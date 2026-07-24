#!/bin/bash
set -euxo pipefail
# Minimal ext4 rootfs whose init runs a heartbeat: a counter kept ONLY in guest
# RAM (tmpfs). After a snapshot/restore migration the counter must continue from
# where it was -- that is the memory-continuity proof. If memory were lost the
# guest would reboot and the counter would restart at 0.
OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"
ROOT=$(mktemp -d)
mkdir -p "$ROOT"/{bin,sbin,proc,sys,dev,tmp,etc}

BB=$(command -v busybox || echo /bin/busybox)
cp "$BB" "$ROOT/bin/busybox"
for a in sh mount umount cat echo sleep ls mkdir dmesg ln; do
  ln -sf busybox "$ROOT/bin/$a"
done

cat > "$ROOT/init" <<'INIT'
#!/bin/busybox sh
/bin/busybox mount -t proc proc /proc 2>/dev/null || true
/bin/busybox mount -t sysfs sys /sys 2>/dev/null || true
/bin/busybox mount -t tmpfs tmpfs /tmp 2>/dev/null || true
echo "GUEST_BOOTED $(cat /proc/sys/kernel/random/boot_id 2>/dev/null || echo noid)"
COUNTER=/tmp/heartbeat_counter
[ -f "$COUNTER" ] || echo 0 > "$COUNTER"
while true; do
  n=$(cat "$COUNTER")
  n=$((n+1))
  echo "$n" > "$COUNTER"
  echo "HEARTBEAT $n"
  /bin/busybox sleep 1
done
INIT
chmod +x "$ROOT/init"

IMG="$OUT/rootfs.ext4"
rm -f "$IMG"
dd if=/dev/zero of="$IMG" bs=1M count=48 status=none
mkfs.ext4 -q -F -O ^has_journal -L rootfs "$IMG"
MNT=$(mktemp -d)
sudo mount -o loop "$IMG" "$MNT"
sudo cp -a "$ROOT"/. "$MNT"/
sudo umount "$MNT"
rm -rf "$MNT" "$ROOT"
ls -la "$IMG"
echo ROOTFS_BUILD_DONE
