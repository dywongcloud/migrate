#!/bin/bash
# Build a networked microVM rootfs: init brings up eth0 with a fixed IP/MAC and
# runs the UDP beacon (a compiled static binary sending an increasing sequence
# number). The guest keeps its identity across both hosts, so a collector on the
# host measures the beacon gap across a migration = the user-visible blackout.
set -euxo pipefail
OUT=/Users/dylanwong/daedal/kernel/build
SRC=/Users/dylanwong/daedal/images
GUEST_IP=${GUEST_IP:-172.20.0.2}
HOST_IP=${HOST_IP:-172.20.0.1}
PORT=${PORT:-9999}
INTERVAL_US=${INTERVAL_US:-1000}

gcc -O2 -static -o /tmp/beacon "$SRC/beacon.c"
file /tmp/beacon

ROOT=$(mktemp -d)
mkdir -p "$ROOT"/{bin,sbin,proc,sys,dev,tmp,etc}
BB=$(command -v busybox)
cp "$BB" "$ROOT/bin/busybox"
cp /tmp/beacon "$ROOT/beacon"
for a in sh mount echo sleep ip; do ln -sf busybox "$ROOT/bin/$a"; done

cat > "$ROOT/init" <<INIT
#!/bin/busybox sh
/bin/busybox mount -t proc proc /proc 2>/dev/null || true
/bin/busybox mount -t sysfs sys /sys 2>/dev/null || true
/bin/busybox ip link set lo up 2>/dev/null || true
/bin/busybox ip addr add ${GUEST_IP}/24 dev eth0 2>/dev/null || true
/bin/busybox ip link set eth0 up 2>/dev/null || true
echo "GUEST_BOOTED net \$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || echo noid)"
exec /beacon ${HOST_IP} ${PORT} ${INTERVAL_US}
INIT
chmod +x "$ROOT/init"

IMG="$OUT/rootfs-net.ext4"; rm -f "$IMG"
dd if=/dev/zero of="$IMG" bs=1M count=48 status=none
mkfs.ext4 -q -F -O ^has_journal -L rootfs "$IMG"
MNT=$(mktemp -d)
sudo mount -o loop "$IMG" "$MNT"
sudo cp -a "$ROOT"/. "$MNT"/
sudo umount "$MNT"; rm -rf "$MNT" "$ROOT"
ls -la "$IMG"
echo NET_ROOTFS_DONE
