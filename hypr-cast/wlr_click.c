#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <wayland-client.h>
#include "wlr-virtual-pointer-v1-client-protocol.h"

struct wl_display *display = NULL;
struct wl_registry *registry = NULL;
struct zwlr_virtual_pointer_manager_v1 *pointer_manager = NULL;
struct zwlr_virtual_pointer_v1 *virtual_pointer = NULL;
struct wl_seat *target_seat = NULL;
struct wl_output *headless_output = NULL;

struct output_entry {
    struct wl_output *output;
    uint32_t global_name;
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
            headless_output = entry->output;
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
    if (strcmp(interface, zwlr_virtual_pointer_manager_v1_interface.name) == 0) {
        uint32_t ver = version >= 2 ? 2 : 1;
        pointer_manager = wl_registry_bind(registry, name,
            &zwlr_virtual_pointer_manager_v1_interface, ver);
    } else if (strcmp(interface, "wl_seat") == 0) {
        if (!target_seat) {
            target_seat = wl_registry_bind(registry, name, &wl_seat_interface, 1);
        }
    } else if (strcmp(interface, "wl_output") == 0) {
        if (output_count < MAX_OUTPUTS) {
            uint32_t ver = version >= 4 ? 4 : version;
            struct wl_output *out = wl_registry_bind(registry, name, &wl_output_interface, ver);
            outputs[output_count].output = out;
            outputs[output_count].global_name = name;
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

static uint32_t get_time_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint32_t)(ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
}

int main(int argc, char *argv[]) {
    if (argc < 6) {
        fprintf(stderr, "Usage: %s <rel_x_0_to_1> <rel_y_0_to_1> <res_w> <res_h> <click|rightclick|down|move|up>\n", argv[0]);
        return 1;
    }

    double rel_x = atof(argv[1]);
    double rel_y = atof(argv[2]);
    uint32_t res_w = atoi(argv[3]);
    uint32_t res_h = atoi(argv[4]);
    const char *action = argv[5];

    if (rel_x < 0.0) rel_x = 0.0; if (rel_x > 1.0) rel_x = 1.0;
    if (rel_y < 0.0) rel_y = 0.0; if (rel_y > 1.0) rel_y = 1.0;

    uint32_t local_x = (uint32_t)(rel_x * res_w);
    uint32_t local_y = (uint32_t)(rel_y * res_h);

    display = wl_display_connect(NULL);
    if (!display) return 1;

    registry = wl_display_get_registry(display);
    wl_registry_add_listener(registry, &registry_listener, NULL);
    wl_display_roundtrip(display); // Process globals
    wl_display_roundtrip(display); // Process output names

    if (!pointer_manager) {
        wl_display_disconnect(display);
        return 1;
    }

    uint32_t mgr_version = wl_proxy_get_version((struct wl_proxy *)pointer_manager);
    if (mgr_version >= 2 && headless_output) {
        virtual_pointer = zwlr_virtual_pointer_manager_v1_create_virtual_pointer_with_output(
            pointer_manager, target_seat, headless_output);
    } else {
        virtual_pointer = zwlr_virtual_pointer_manager_v1_create_virtual_pointer(
            pointer_manager, target_seat);
    }
    wl_display_roundtrip(display);

    uint32_t t = get_time_ms();

    // Motion relative to HEADLESS-3 output (0..res_w, 0..res_h)
    zwlr_virtual_pointer_v1_motion_absolute(virtual_pointer, t, local_x, local_y, res_w, res_h);
    zwlr_virtual_pointer_v1_frame(virtual_pointer);

    if (strcmp(action, "click") == 0) {
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x110, WL_POINTER_BUTTON_STATE_PRESSED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
        wl_display_flush(display);
        struct timespec delay = { .tv_sec = 0, .tv_nsec = 30000000 };
        nanosleep(&delay, NULL);
        t = get_time_ms();
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x110, WL_POINTER_BUTTON_STATE_RELEASED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
    } else if (strcmp(action, "rightclick") == 0) {
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x111, WL_POINTER_BUTTON_STATE_PRESSED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
        wl_display_flush(display);
        struct timespec delay = { .tv_sec = 0, .tv_nsec = 30000000 };
        nanosleep(&delay, NULL);
        t = get_time_ms();
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x111, WL_POINTER_BUTTON_STATE_RELEASED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
    } else if (strcmp(action, "down") == 0) {
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x110, WL_POINTER_BUTTON_STATE_PRESSED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
    } else if (strcmp(action, "up") == 0) {
        zwlr_virtual_pointer_v1_button(virtual_pointer, t, 0x110, WL_POINTER_BUTTON_STATE_RELEASED);
        zwlr_virtual_pointer_v1_frame(virtual_pointer);
    }

    wl_display_flush(display);
    wl_display_roundtrip(display);

    zwlr_virtual_pointer_v1_destroy(virtual_pointer);
    zwlr_virtual_pointer_manager_v1_destroy(pointer_manager);
    if (target_seat) wl_seat_destroy(target_seat);
    for (int i = 0; i < output_count; i++) {
        wl_output_destroy(outputs[i].output);
    }
    wl_registry_destroy(registry);
    wl_display_disconnect(display);

    return 0;
}
