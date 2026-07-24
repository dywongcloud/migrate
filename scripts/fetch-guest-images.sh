#!/bin/bash
set -euxo pipefail
ARCH="$1"   # x86_64 | aarch64
OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"
CI=https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.12/$ARCH

# Guest kernel (uncompressed vmlinux the KVM path boots directly).
KVER=$(curl -fsSL "$CI/vmlinux-6.1.bin" -o "$OUT/vmlinux-$ARCH" && echo ok || echo fail)
if [ "$KVER" != ok ]; then
  # fall back to whatever vmlinux the bucket lists
  curl -fsSL "$CI/vmlinux.bin" -o "$OUT/vmlinux-$ARCH"
fi

# Rootfs squashfs -> ext4 (writable, needed for our heartbeat agent).
curl -fsSL "$CI/ubuntu-24.04.squashfs" -o "$OUT/rootfs-$ARCH.squashfs"

file "$OUT/vmlinux-$ARCH"
ls -la "$OUT"/vmlinux-"$ARCH" "$OUT"/rootfs-"$ARCH".squashfs
echo FETCH_DONE_$ARCH
