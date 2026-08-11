#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <time.h>
#include <poll.h>
#include <jpeglib.h>
#include <wayland-client.h>
#include "wlr-screencopy-v1-client-protocol.h"

struct wl_display *display = NULL;
struct wl_registry *registry = NULL;
struct wl_shm *shm = NULL;
struct zwlr_screencopy_manager_v1 *screencopy_mgr = NULL;
struct wl_output *target_output = NULL;

struct output_entry {
    struct wl_output *output;
    char name[64];
};

#define MAX_OUTPUTS 16
static struct output_entry outputs[MAX_OUTPUTS];
static int output_count = 0;

static void output_geometry(void *data, struct wl_output *wl_output, int32_t x, int32_t y,
                            int32_t physical_width, int32_t physical_height, int32_t subpixel,
                            const char *make, const char *model, int32_t transform) {}
static void output_mode(void *data, struct wl_output *wl_output, uint32_t flags,
                        int32_t width, int32_t height, int32_t refresh) {}
static void output_done(void *data, struct wl_output *wl_output) {}
static void output_scale(void *data, struct wl_output *wl_output, int32_t factor) {}
static char *target_name = NULL;

static void output_name(void *data, struct wl_output *wl_output, const char *name) {
    struct output_entry *entry = (struct output_entry *)data;
    if (entry && name) {
        snprintf(entry->name, sizeof(entry->name), "%s", name);
        if ((target_name && strcmp(name, target_name) == 0) || (!target_name && strncmp(name, "HEADLESS-", 9) == 0)) {
            target_output = entry->output;
        }
    }
}
static void output_description(void *data, struct wl_output *wl_output, const char *description) {}

static const struct wl_output_listener output_listener = {
    .geometry = output_geometry,
    .mode = output_mode,
    .done = output_done,
    .scale = output_scale,
    .name = output_name,
    .description = output_description,
};

static void handle_global(void *data, struct wl_registry *registry,
                          uint32_t name, const char *interface, uint32_t version) {
    if (strcmp(interface, zwlr_screencopy_manager_v1_interface.name) == 0) {
        screencopy_mgr = wl_registry_bind(registry, name, &zwlr_screencopy_manager_v1_interface, 1);
    } else if (strcmp(interface, "wl_shm") == 0) {
        shm = wl_registry_bind(registry, name, &wl_shm_interface, 1);
    } else if (strcmp(interface, "wl_output") == 0) {
        if (output_count < MAX_OUTPUTS) {
            uint32_t ver = version >= 4 ? 4 : version;
            struct wl_output *out = wl_registry_bind(registry, name, &wl_output_interface, ver);
            outputs[output_count].output = out;
            outputs[output_count].name[0] = '\0';
            wl_output_add_listener(out, &output_listener, &outputs[output_count]);
            output_count++;
        }
    }
}

static void handle_global_remove(void *data, struct wl_registry *registry, uint32_t name) {}

static const struct wl_registry_listener registry_listener = {
    .global = handle_global,
    .global_remove = handle_global_remove,
};

struct frame_buffer {
    uint32_t format;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
    size_t size;
    int fd;
    void *data;
    struct wl_buffer *wl_buf;
    int ready;
    int failed;
};

static void frame_handle_buffer(void *data, struct zwlr_screencopy_frame_v1 *frame,
                                uint32_t format, uint32_t width, uint32_t height, uint32_t stride) {
    struct frame_buffer *fb = (struct frame_buffer *)data;
    fb->format = format;
    fb->width = width;
    fb->height = height;
    fb->stride = stride;
    fb->size = stride * height;

    fb->fd = memfd_create("screencopy-buffer", MFD_CLOEXEC);
    if (fb->fd < 0) return;
    if (ftruncate(fb->fd, fb->size) < 0) return;

    fb->data = mmap(NULL, fb->size, PROT_READ | PROT_WRITE, MAP_SHARED, fb->fd, 0);
    if (fb->data == MAP_FAILED) return;

    struct wl_shm_pool *pool = wl_shm_create_pool(shm, fb->fd, fb->size);
    fb->wl_buf = wl_shm_pool_create_buffer(pool, 0, width, height, stride, format);
    wl_shm_pool_destroy(pool);

    zwlr_screencopy_frame_v1_copy(frame, fb->wl_buf);
}

static void frame_handle_flags(void *data, struct zwlr_screencopy_frame_v1 *frame, uint32_t flags) {}
static void frame_handle_ready(void *data, struct zwlr_screencopy_frame_v1 *frame,
                               uint32_t tv_sec_hi, uint32_t tv_sec_lo, uint32_t tv_nsec) {
    struct frame_buffer *fb = (struct frame_buffer *)data;
    fb->ready = 1;
}
static void frame_handle_failed(void *data, struct zwlr_screencopy_frame_v1 *frame) {
    struct frame_buffer *fb = (struct frame_buffer *)data;
    fb->failed = 1;
}

static const struct zwlr_screencopy_frame_v1_listener frame_listener = {
    .buffer = frame_handle_buffer,
    .flags = frame_handle_flags,
    .ready = frame_handle_ready,
    .failed = frame_handle_failed,
};

static void encode_and_write_jpeg(unsigned char *bgra, int width, int height, int stride, int quality) {
    struct jpeg_compress_struct cinfo;
    struct jpeg_error_mgr jerr;
    unsigned char *out_buf = NULL;
    unsigned long out_size = 0;

    cinfo.err = jpeg_std_error(&jerr);
    jpeg_create_compress(&cinfo);
    jpeg_mem_dest(&cinfo, &out_buf, &out_size);

    cinfo.image_width = width;
    cinfo.image_height = height;
    cinfo.input_components = 4;
    cinfo.in_color_space = JCS_EXT_BGRA;

    jpeg_set_defaults(&cinfo);
    jpeg_set_quality(&cinfo, quality, TRUE);

    // Full 4:4:4 chroma sampling for 100% vibrant sRGB color precision (no YUV 4:2:0 subsampling loss)
    cinfo.comp_info[0].h_samp_factor = 1;
    cinfo.comp_info[0].v_samp_factor = 1;
    cinfo.comp_info[1].h_samp_factor = 1;
    cinfo.comp_info[1].v_samp_factor = 1;
    cinfo.comp_info[2].h_samp_factor = 1;
    cinfo.comp_info[2].v_samp_factor = 1;

    jpeg_start_compress(&cinfo, TRUE);

    while (cinfo.next_scanline < cinfo.image_height) {
        JSAMPROW row_pointer = bgra + (cinfo.next_scanline * stride);
        jpeg_write_scanlines(&cinfo, &row_pointer, 1);
    }

    jpeg_finish_compress(&cinfo);
    jpeg_destroy_compress(&cinfo);

    if (out_buf && out_size > 0) {
        uint32_t len = (uint32_t)out_size;
        if (fwrite(&len, sizeof(uint32_t), 1, stdout) == 1) {
            fwrite(out_buf, 1, out_size, stdout);
            fflush(stdout);
        }
        free(out_buf);
    }
}

int main(int argc, char *argv[]) {
    if (argc > 1) {
        target_name = argv[1];
    }
    display = wl_display_connect(NULL);
    if (!display) {
        fprintf(stderr, "Failed to connect to Wayland display\n");
        return 1;
    }

    registry = wl_display_get_registry(display);
    wl_registry_add_listener(registry, &registry_listener, NULL);
    wl_display_roundtrip(display);
    wl_display_roundtrip(display);

    if (!screencopy_mgr || !shm || !target_output) {
        fprintf(stderr, "Missing Wayland interfaces or HEADLESS output\n");
        return 1;
    }

    int consecutive_failures = 0;

    // Continuous capture loop (~60 FPS)
    while (1) {
        struct frame_buffer fb = {0};
        struct zwlr_screencopy_frame_v1 *frame =
            zwlr_screencopy_manager_v1_capture_output(screencopy_mgr, 0, target_output);

        zwlr_screencopy_frame_v1_add_listener(frame, &frame_listener, &fb);

        struct pollfd pfd = {
            .fd = wl_display_get_fd(display),
            .events = POLLIN,
        };

        while (!fb.ready && !fb.failed) {
            wl_display_dispatch_pending(display);
            if (fb.ready || fb.failed) break;

            if (wl_display_flush(display) < 0) {
                fb.failed = 1;
                break;
            }

            int ret = poll(&pfd, 1, 500);
            if (ret <= 0) {
                fprintf(stderr, "wlr_jpeg: frame timeout (compositor stalled or display reconfigured)\n");
                fb.failed = 1;
                break;
            }

            if (wl_display_dispatch(display) < 0) {
                fb.failed = 1;
                break;
            }
        }

        if (fb.ready && fb.data && fb.size > 0) {
            consecutive_failures = 0;
            encode_and_write_jpeg((unsigned char *)fb.data, fb.width, fb.height, fb.stride, 75);
        } else {
            consecutive_failures++;
            if (consecutive_failures >= 3) {
                fprintf(stderr, "wlr_jpeg: 3 consecutive frame failures, exiting to allow server restart\n");
                if (fb.wl_buf) wl_buffer_destroy(fb.wl_buf);
                if (fb.data && fb.data != MAP_FAILED) munmap(fb.data, fb.size);
                if (fb.fd >= 0) close(fb.fd);
                zwlr_screencopy_frame_v1_destroy(frame);
                break;
            }
        }

        if (fb.wl_buf) wl_buffer_destroy(fb.wl_buf);
        if (fb.data && fb.data != MAP_FAILED) munmap(fb.data, fb.size);
        if (fb.fd >= 0) close(fb.fd);
        zwlr_screencopy_frame_v1_destroy(frame);

        // Frame rate limiter (~60 FPS)
        usleep(4000);
    }

    wl_display_disconnect(display);
    return 0;
}
