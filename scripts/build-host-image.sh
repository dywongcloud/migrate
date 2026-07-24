#!/bin/bash
# Build a self-contained container rootfs for a Firecracker "host" without pulling
# a base image (Lima's NAT makes registry pulls unreliable). Assembles firecracker,
# its shared libraries, busybox, and the orchestrator into a directory that podman
# runs directly via --rootfs.
set -euxo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
DST=${1:-$HOME/host-rootfs}
B=/Users/dylanwong/daedal/kernel/build
sudo podman rm -f host-a host-b >/dev/null 2>&1 || true
case "${DST:-}" in ""|/|"$HOME"|"$HOME/") echo "refusing to sudo rm -rf '$DST'" >&2; exit 1;; esac
sudo rm -rf "$DST"; mkdir -p "$DST"/{bin,lib,lib64,usr/lib,proc,sys,dev,tmp,shared,etc}

install -m0755 "$B/firecracker-aarch64" "$DST/bin/firecracker"
install -m0755 /Users/dylanwong/daedal/bin/daedald-linux-arm64 "$DST/bin/daedald"
UFFD=$(find ~/fc/firecracker-next/build -name uffd_on_demand_handler -type f -executable | head -1)
install -m0755 "$UFFD" "$DST/bin/uffd_on_demand_handler"
cp "$(command -v busybox)" "$DST/bin/busybox"
for a in sh ls cat sleep grep; do ln -sf busybox "$DST/bin/$a"; done

# copy the shared libraries firecracker + daedald need
for bin in "$DST/bin/firecracker" "$DST/bin/uffd_on_demand_handler"; do
  ldd "$bin" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i ~ /^\//) print $i}' | while read -r lib; do
    d="$DST$(dirname "$lib")"; mkdir -p "$d"; cp -Lu "$lib" "$d/" 2>/dev/null || true
  done
done
# the dynamic loader
cp -Lu /lib/ld-linux-aarch64.so.1 "$DST/lib/" 2>/dev/null || true

echo 'nameserver 1.1.1.1' > "$DST/etc/resolv.conf"
du -sh "$DST"
echo HOST_IMAGE_DONE
