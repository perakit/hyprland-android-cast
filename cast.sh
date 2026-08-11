#!/usr/bin/env bash
set -e

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/hypr-cast"
WIFI_IF=$(iw dev 2>/dev/null | awk '$1=="Interface"{print $2; exit}' || echo "wlp4s0")

# 1. Ensure Hyprland headless monitor exists
if ! hyprctl monitors | grep -q "HEADLESS-"; then
    echo "Creating virtual display..."
    hyprctl output create headless
fi

# 2. Re-apply current active monitor layout config
if [ -f "${HOME}/.config/hypr/configs/monitor.conf" ]; then
    echo "Applying monitor configuration from monitor.conf..."
    hyprctl reload >/dev/null 2>&1 || true
fi

# 3. Generate Wayland screencopy headers if needed
if [ ! -f "${PROJECT_DIR}/wlr-screencopy-v1-client-protocol.h" ]; then
    if [ ! -f "${PROJECT_DIR}/wlr-screencopy-v1.xml" ]; then
        curl -s https://gitlab.freedesktop.org/wlroots/wlr-protocols/-/raw/master/unstable/wlr-screencopy-unstable-v1.xml -o "${PROJECT_DIR}/wlr-screencopy-v1.xml"
    fi
    wayland-scanner client-header "${PROJECT_DIR}/wlr-screencopy-v1.xml" "${PROJECT_DIR}/wlr-screencopy-v1-client-protocol.h"
    wayland-scanner private-code "${PROJECT_DIR}/wlr-screencopy-v1.xml" "${PROJECT_DIR}/wlr-screencopy-v1-protocol.c"
fi

# 4. Build wlr_jpeg helper if needed
if [ ! -f "${PROJECT_DIR}/wlr_jpeg" ] || [ "${PROJECT_DIR}/wlr_jpeg.c" -nt "${PROJECT_DIR}/wlr_jpeg" ]; then
    echo "Building native Wayland sRGB 4:4:4 libjpeg-turbo helper..."
    gcc -O2 "${PROJECT_DIR}/wlr_jpeg.c" "${PROJECT_DIR}/wlr-screencopy-v1-protocol.c" -I"${PROJECT_DIR}" -lwayland-client -ljpeg -o "${PROJECT_DIR}/wlr_jpeg"
fi

# 5. Build wlr_click if needed
if [ ! -f "${PROJECT_DIR}/wlr_click" ] || [ "${PROJECT_DIR}/wlr_click.c" -nt "${PROJECT_DIR}/wlr_click" ]; then
    echo "Building native Wayland virtual pointer helper..."
    if [ ! -f "${PROJECT_DIR}/wlr-virtual-pointer-v1.xml" ]; then
        curl -s https://gitlab.freedesktop.org/wlroots/wlr-protocols/-/raw/master/unstable/wlr-virtual-pointer-unstable-v1.xml -o "${PROJECT_DIR}/wlr-virtual-pointer-v1.xml"
    fi
    wayland-scanner client-header "${PROJECT_DIR}/wlr-virtual-pointer-v1.xml" "${PROJECT_DIR}/wlr-virtual-pointer-v1-client-protocol.h"
    wayland-scanner private-code "${PROJECT_DIR}/wlr-virtual-pointer-v1.xml" "${PROJECT_DIR}/wlr-virtual-pointer-v1-protocol.c"
    gcc -O2 "${PROJECT_DIR}/wlr_click.c" "${PROJECT_DIR}/wlr-virtual-pointer-v1-protocol.c" -I"${PROJECT_DIR}" -lwayland-client -o "${PROJECT_DIR}/wlr_click"
fi

# 6. Build Go server if missing or updated
if [ ! -f "${PROJECT_DIR}/cast_server" ] || [ "${PROJECT_DIR}/main.go" -nt "${PROJECT_DIR}/cast_server" ]; then
    echo "Building native Go streaming server..."
    go build -o "${PROJECT_DIR}/cast_server" "${PROJECT_DIR}/main.go"
fi

# 7. Enable high-performance streaming power profile & disable Wi-Fi power save
echo "Enabling high-performance streaming profile & disabling Wi-Fi power save..."
sudo iw dev "${WIFI_IF}" set power_save off >/dev/null 2>&1 || true
sudo tuned-adm profile throughput-performance >/dev/null 2>&1 || true

# 8. Stop existing watcher and stream server processes
pgrep -f "[c]ast_watcher" | xargs -r kill
pgrep -f "[m]jpeg_server.py|[c]ast_server" | xargs -r kill

# 9. Start Go server in background
setsid "${PROJECT_DIR}/cast_server" "$@" > "${PROJECT_DIR}/cast_server.log" 2>&1 < /dev/null & disown

# 10. Start background watcher to auto-restore power saving when server exits
(
    exec -a cast_watcher bash -c '
        while pgrep -f "[c]ast_server" >/dev/null; do
            sleep 2
        done
        sudo iw dev "'"${WIFI_IF}"'" set power_save on >/dev/null 2>&1 || true
        sudo tuned-adm profile balanced >/dev/null 2>&1 || true
    '
) >/dev/null 2>&1 & disown

# 11. Report status
sleep 0.5
PIN_OUTPUT=$(grep -i "PIN:" "${PROJECT_DIR}/cast_server.log" | tail -n 1)
HOST_IP=$(hostname -I | awk '{print $1}')
echo "=========================================="
echo "High-Performance Go Cast server ready!"
echo "Stream URL: http://${HOST_IP}:8089"
if [ -n "$PIN_OUTPUT" ]; then
    echo "${PIN_OUTPUT}"
fi
echo "Project Location: ${PROJECT_DIR}"
echo "=========================================="
