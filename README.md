# Hypr-Cast (Screen Casting & Remote Control)

Ultra low-latency Wi-Fi screen streaming server for **Hyprland (Wayland)** paired with a native **Android** tablet receiver app. Features multi-touch gesture injection, dynamic resolution matching, 4:4:4 sRGB color accuracy, and hardware keyboard forwarding.

---

## 🚀 Features

- **Ultra Low Latency Capture**: Native C Wayland continuous screencopy (`zwlr_screencopy_manager_v1`) with 4:4:4 sRGB `libjpeg-turbo` encoding (~1.5ms frame encode time).
- **High-Performance Go Server**: Efficient HTTP MJPEG streaming with automatic rate-limiting and network power profile management.
- **Dynamic Resolution Scaling**: Auto-resizes the virtual headless display in Hyprland to match the connected client's aspect ratio and screen resolution.
- **Interactive Multi-Touch & Mouse**: Native C Wayland client using `zwlr_virtual_pointer_manager_v1` for precise touch coordinate mapping and click forwarding.
- **Hardware Keyboard Forwarding**: Forwards shortcuts and key events via Hyprland's native `sendshortcut` dispatcher and `wtype`.
- **PIN Session Security**: Built-in 6-digit session PIN authentication, desktop connection alerts via `notify-send`, and automatic IP brute-force lockout.
- **Native Android Client**: Custom Android app built with **Kotlin** and **Jetpack Compose** for low-overhead viewing and full touch interaction.

---

## 📁 Repository Structure

```text
.
├── cast.sh               # Main launcher script (creates headless monitor, builds helpers, starts server)
├── stop_cast.sh          # Stops server processes and restores system power settings
├── hypr-cast/            # Linux Wayland streaming server & input engine
│   ├── main.go           # High-performance Go HTTP streaming server
│   ├── wlr_jpeg.c        # Native Wayland sRGB screencopy & JPEG encoder
│   ├── wlr_click.c       # Native Wayland virtual pointer helper
│   └── wlr_stream.c      # Raw Wayland screencopy helper
└── android/              # Android client application
    ├── app/              # Jetpack Compose UI, ViewModel, DataStore repository
    └── install.sh        # Gradle build & ADB install script
```

---

## 🛠️ Prerequisites

### Linux Host (Server)
- **Compositor**: Hyprland (Wayland)
- **Tools & Libraries**: `go`, `gcc`, `libjpeg-turbo`, `wayland-scanner`, `wayland-client-devel` (or `libwayland-dev`), `iw`
- **Optional**: `tuned` (for `tuned-adm` throughput performance profiles), `wtype`, `libnotify` (`notify-send`)

### Android Client
- Android 12.0+ (API level 31 or higher)
- Android Studio or Gradle 8.x+ with Android SDK installed

---

## ⚡ Quick Start

### 1. Start the Server (Linux Host)

Run the root `cast.sh` script to set up the virtual headless display, build binary helpers, and launch the server:

```bash
./cast.sh
```

- On startup, a random 6-digit **PIN** is generated and sent via desktop notification.
- To set a custom PIN:
  ```bash
  CAST_PIN=123456 ./cast.sh
  ```
- The stream will be hosted at `http://<HOST_IP>:8089`.

### 2. Stop the Server

To shut down the server and restore Wi-Fi power savings:

```bash
./stop_cast.sh
```

### 3. Build & Install the Android App

Connect your Android device via USB/ADB and run:

```bash
cd android
./install.sh
```

Or open the `android/` directory in **Android Studio** and build the release/debug target directly.

---

## 🔒 Security & Authentication

- **Session PIN Authentication**: Requires authentication via `/login` before accessing the video stream or touch/keyboard endpoints.
- **Brute-Force Protection**: 5 failed PIN attempts trigger a 5-minute IP address lockout (`HTTP 429 Too Many Requests`).
- **Desktop Alerts**: Sends immediate desktop notifications whenever a client connects, authenticates, or triggers security rate limits.

---

## 🌐 HTTP API Endpoints

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `GET /login` / `POST /login` | Form | PIN authentication page & session handler |
| `GET /stream` | Streaming | `multipart/x-mixed-replace` 60 FPS MJPEG stream |
| `GET /resize?w=<W>&h=<H>` | Action | Resizes Hyprland headless monitor resolution |
| `GET /touch?x=<X>&y=<Y>&action=<ACT>` | Action | Triggers Wayland virtual pointer motion/clicks |
| `GET /key?key=<K>&code=<C>&...` | Action | Forwards hardware keyboard input to Hyprland |
| `GET /logout` | Action | Invalidates current session cookie |

---

## 📜 License

Distributed under the project's standard open-source conventions.
