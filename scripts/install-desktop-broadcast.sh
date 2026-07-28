#!/bin/bash
# Injects the desktop-broadcast binary and a systemd unit for it into the
# desktop guest's rootfs image, and permanently fixes vncdesktop.service
# (TigerVNC refuses -SecurityTypes None -localhost no without
# --I-KNOW-THIS-IS-INSECURE, and the original Type=forking unit's PIDFile
# never gets created by this TigerVNC version -- Type=simple + vncserver -fg
# is what actually reaches systemd "active"). Idempotent: safe to re-run
# after a fresh build-desktop-rootfs.sh image or a desktop-broadcast rebuild.
set -euo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux mkfs.ext4

ROOT=/Users/dylanwong/daedal
IMG=${IMG:-$ROOT/kernel/build/rootfs-desktop.ext4}
BIN=${BIN:-$ROOT/gateway/target/release/desktop-broadcast}
VNC_GEOMETRY=${VNC_GEOMETRY:-1280x800}
DISPLAY_NUM=${DISPLAY_NUM:-:1}

[ -f "$IMG" ] || { echo "ERROR: $IMG not found, run build-desktop-rootfs.sh first" >&2; exit 1; }
[ -x "$BIN" ] || { echo "ERROR: $BIN not found, run: cd gateway && cargo build --release -p desktop-broadcast" >&2; exit 1; }

sudo pkill -f "api-sock.*fc-desktop" 2>/dev/null || true
sleep 0.5

MNT=$(mktemp -d)
cleanup() { sudo umount -lf "$MNT" 2>/dev/null || true; rmdir "$MNT" 2>/dev/null || true; }
trap cleanup EXIT
sudo mount -o loop "$IMG" "$MNT"

sudo install -m 0755 "$BIN" "$MNT/usr/local/bin/desktop-broadcast"

cat <<UNIT | sudo tee "$MNT/etc/systemd/system/vncdesktop.service" >/dev/null
[Unit]
Description=XFCE desktop over VNC
After=network.target

[Service]
Type=simple
User=vncuser
ExecStart=/usr/bin/vncserver -fg :1 -geometry $VNC_GEOMETRY -SecurityTypes None -localhost no --I-KNOW-THIS-IS-INSECURE
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

cat <<UNIT | sudo tee "$MNT/etc/systemd/system/desktop-broadcast.service" >/dev/null
[Unit]
Description=Tier-2 H.264/MoQ desktop broadcast over iroh-live
After=vncdesktop.service
Requires=vncdesktop.service

[Service]
Type=simple
User=vncuser
Environment=DISPLAY=$DISPLAY_NUM
Environment=XAUTHORITY=/home/vncuser/.Xauthority
WorkingDirectory=/home/vncuser
ExecStartPre=/bin/sleep 3
ExecStart=/usr/local/bin/desktop-broadcast --keyfile /home/vncuser/desktop-broadcast.key --name desktop
Restart=on-failure
RestartSec=3
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

sudo mkdir -p "$MNT/etc/systemd/system/multi-user.target.wants"
sudo ln -sf /etc/systemd/system/desktop-broadcast.service \
  "$MNT/etc/systemd/system/multi-user.target.wants/desktop-broadcast.service"
sudo ln -sf /etc/systemd/system/vncdesktop.service \
  "$MNT/etc/systemd/system/multi-user.target.wants/vncdesktop.service"

sudo umount "$MNT"
trap - EXIT
rmdir "$MNT"
echo INSTALL_DESKTOP_BROADCAST_DONE
