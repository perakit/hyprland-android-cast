# Hypr-Cast (Virtual Display Cast Server)

Low-latency Wi-Fi screen streaming server for Hyprland (Wayland) to Android tablets with interactive multi-touch and hardware keyboard forwarding.

## Project Structure

- `main.go`: High-performance Go HTTP streaming server (port `8089`). Handles MJPEG streaming, dynamic viewport resolution scaling, touch normalized coordinate translation, and keyboard shortcut forwarding via Hyprland native `sendshortcut`.
- `wlr_jpeg.c`: Native C Wayland continuous screencopy (`zwlr_screencopy_manager_v1`) & 4:4:4 sRGB `libjpeg-turbo` encoder (~1.5ms encode time, 100% desktop sRGB color accuracy).
- `wlr_click.c`: Native C Wayland client using `zwlr_virtual_pointer_manager_v1` (v4 output binding) to issue virtual pointer motion and click events targeted strictly to `HEADLESS-3`.
- `memories/hyprland-cast-shortcuts.md`: Detailed architecture memory covering Electron VS Code shortcut fixes, Android scancode mangling solutions, and 4:4:4 sRGB capture.
- `cast.sh`: Launcher script that sets up `HEADLESS-3`, builds binaries if needed, and starts `cast_server`.

## Authentication & Security

Hypr-Cast uses a session-based PIN authentication system with built-in brute-force protection:
- **Auto-generated PIN:** On startup, a random 6-digit PIN is generated, displayed in the console / logs, and sent to your desktop via `notify-send`.
- **Custom PIN:** Set a custom PIN using `HYPRCAST_PIN` or `CAST_PIN` environment variables or the `-pin` flag:
  ```bash
  CAST_PIN=123456 ./cast.sh
  # Or directly:
  ./hypr-cast/cast_server -pin 123456
  ```
- **Desktop Connection Alerts:** Sends instant desktop notifications (`notify-send`) when a client successfully authenticates or starts streaming screen frames (with client IP address).
- **Brute-Force Protection & Auto-Lockout:**
  - **Attempt Limit:** 5 consecutive failed PIN attempts per IP address.
  - **Lockout Duration:** 5-minute IP address lockout upon exceeding attempt limit (`HTTP 429 Too Many Requests`).
  - **Security Alerts:** Sends critical desktop notifications if an IP triggers brute-force lockout.

## HTTP API Endpoints

- `GET /login` / `POST /login`: Serves PIN login page / authenticates session token cookie.
- `GET /logout`: Clears active session cookie.
- `GET /`: Serves web client HTML/JS with touch & keyboard handlers (requires auth).
- `GET /stream`: `multipart/x-mixed-replace` 60 FPS MJPEG stream captured from `HEADLESS-3` using `wlr_jpeg` (requires auth).
- `GET /resize?w=<width>&h=<height>`: Dynamically adjusts `HEADLESS-3` resolution in Hyprland to match net client aspect ratio (requires auth).
- `GET /touch?x=<0..1>&y=<0..1>&action=<click|rightclick|down|move|up>`: Triggers virtual pointer events via `wlr_click` (requires auth).
- `GET /key?key=<KeyName>&code=<Code>&ctrl=<0|1>&alt=<0|1>&shift=<0|1>&meta=<0|1>&action=down`: Forwards hardware keyboard shortcuts using Hyprland `sendshortcut` and `wtype` (requires auth).

## Quick Start

```bash
./cast.sh
```
