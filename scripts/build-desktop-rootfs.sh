#!/bin/bash
set -euxo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux debootstrap mkfs.ext4 chroot

OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"

DEBIAN_SUITE=${DEBIAN_SUITE:-trixie}
DEBIAN_MIRROR=${DEBIAN_MIRROR:-http://deb.debian.org/debian}
GUEST_IP=${GUEST_IP:-172.20.0.3}
GATEWAY_IP=${GATEWAY_IP:-172.20.0.1}
NETMASK=${NETMASK:-255.255.255.0}
IMG_SIZE_MB=${IMG_SIZE_MB:-2048}
VNC_GEOMETRY=${VNC_GEOMETRY:-1280x800}
ROOT_PASSWORD=${ROOT_PASSWORD:-daedal}
IMG_NAME=${IMG_NAME:-rootfs-desktop}
GUEST_HOSTNAME=${GUEST_HOSTNAME:-daedal-desktop}

case "$(uname -m)" in
  aarch64) DEB_ARCH=arm64 ;;
  x86_64) DEB_ARCH=amd64 ;;
  *)
    echo "ERROR: unsupported host arch $(uname -m) for debootstrap" >&2
    exit 1
    ;;
esac

ROOT=$(mktemp -d)
cleanup() {
  sudo umount -lf "$ROOT/dev/pts" 2>/dev/null || true
  sudo umount -lf "$ROOT/dev" 2>/dev/null || true
  sudo umount -lf "$ROOT/proc" 2>/dev/null || true
  sudo umount -lf "$ROOT/sys" 2>/dev/null || true
  sudo rm -rf "$ROOT"
}
trap cleanup EXIT

sudo debootstrap --arch="$DEB_ARCH" "$DEBIAN_SUITE" "$ROOT" "$DEBIAN_MIRROR"

sudo mount -t proc proc "$ROOT/proc"
sudo mount -t sysfs sys "$ROOT/sys"
sudo mount -o bind /dev "$ROOT/dev"
sudo mount -o bind /dev/pts "$ROOT/dev/pts"
sudo cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

sudo chroot "$ROOT" env DEBIAN_FRONTEND=noninteractive apt-get update
sudo chroot "$ROOT" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
  tigervnc-standalone-server xfce4 xfce4-terminal dbus-x11 ifupdown openssh-server

sudo chroot "$ROOT" useradd -m -s /bin/bash vncuser
echo "vncuser:vncuser" | sudo chroot "$ROOT" chpasswd
echo "root:$ROOT_PASSWORD" | sudo chroot "$ROOT" chpasswd

VNC_UID=$(sudo chroot "$ROOT" id -u vncuser)
VNC_GID=$(sudo chroot "$ROOT" id -g vncuser)

sudo install -d -m 700 -o "$VNC_UID" -g "$VNC_GID" "$ROOT/home/vncuser/.vnc"
cat <<'XSTARTUP' | sudo tee "$ROOT/home/vncuser/.vnc/xstartup" >/dev/null
#!/bin/sh
unset SESSION_MANAGER
unset DBUS_SESSION_BUS_ADDRESS
exec startxfce4
XSTARTUP
sudo chmod 755 "$ROOT/home/vncuser/.vnc/xstartup"
sudo chown -R "$VNC_UID:$VNC_GID" "$ROOT/home/vncuser/.vnc"

cat <<UNIT | sudo tee "$ROOT/etc/systemd/system/vncdesktop.service" >/dev/null
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

if [ -n "${GUEST_SSH_PUBKEY:-}" ]; then
  sudo install -d -m 700 "$ROOT/root/.ssh"
  printf '%s\n' "$GUEST_SSH_PUBKEY" | sudo tee "$ROOT/root/.ssh/authorized_keys" >/dev/null
  sudo chmod 600 "$ROOT/root/.ssh/authorized_keys"
fi

sudo chroot "$ROOT" systemctl enable vncdesktop.service
sudo chroot "$ROOT" systemctl enable ssh.service
sudo chroot "$ROOT" systemctl enable serial-getty@ttyS0.service
sudo chroot "$ROOT" systemctl set-default multi-user.target
sudo chroot "$ROOT" systemctl disable lightdm.service 2>/dev/null || true
sudo chroot "$ROOT" systemctl mask plymouth-quit.service plymouth-quit-wait.service

cat <<IFACES | sudo tee "$ROOT/etc/network/interfaces" >/dev/null
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet static
    address $GUEST_IP
    netmask $NETMASK
    gateway $GATEWAY_IP
IFACES

sudo rm -f "$ROOT/etc/resolv.conf"
cat <<RESOLV | sudo tee "$ROOT/etc/resolv.conf" >/dev/null
nameserver 1.1.1.1
nameserver 8.8.8.8
RESOLV

echo "$GUEST_HOSTNAME" | sudo tee "$ROOT/etc/hostname" >/dev/null
printf '127.0.0.1\tlocalhost\n127.0.1.1\t%s\n' "$GUEST_HOSTNAME" | sudo tee "$ROOT/etc/hosts" >/dev/null

sudo chroot "$ROOT" apt-get clean
sudo rm -rf "$ROOT/var/lib/apt/lists/"*

sudo umount -lf "$ROOT/dev/pts"
sudo umount -lf "$ROOT/dev"
sudo umount -lf "$ROOT/proc"
sudo umount -lf "$ROOT/sys"

IMG="$OUT/$IMG_NAME.ext4"
rm -f "$IMG"
dd if=/dev/zero of="$IMG" bs=1M count="$IMG_SIZE_MB" status=none
mkfs.ext4 -q -F -O ^has_journal -L "$IMG_NAME" "$IMG"
MNT=$(mktemp -d)
sudo mount -o loop "$IMG" "$MNT"
sudo cp -a "$ROOT"/. "$MNT"/
sudo chown root:root "$MNT"
sudo chmod 755 "$MNT"
sudo umount "$MNT"
rmdir "$MNT"
ls -la "$IMG"
echo DESKTOP_ROOTFS_DONE
