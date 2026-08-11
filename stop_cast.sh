#!/usr/bin/env bash
echo "Stopping Cast streaming server..."
pgrep -f "[c]ast_watcher" | xargs -r kill
pgrep -f "[c]ast_server" | xargs -r kill
sudo iw dev wlp4s0 set power_save on >/dev/null 2>&1 || true
sudo tuned-adm profile balanced >/dev/null 2>&1 || true
echo "Cast server stopped and power saving settings restored."
