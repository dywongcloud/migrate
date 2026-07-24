#!/bin/bash
set -euxo pipefail
SRC=~/pvm/guest-linux
PATCHES=/Users/dylanwong/daedal/kernel/patches
JOBS=$(nproc)

cd "$SRC"
git checkout -- . 2>/dev/null || true
# Only patch 4 touches the guest kernel (arch/x86/kernel/pvm.c); the others are host-side.
if git apply --check "$PATCHES/0004-x86-pvm-guest-fsbase-via-hypercall.patch" 2>/dev/null; then
  git apply "$PATCHES/0004-x86-pvm-guest-fsbase-via-hypercall.patch"
  echo "APPLIED guest patch 4"
fi

curl -fsSL -o .config https://raw.githubusercontent.com/virt-pvm/misc/main/pvm-guest-6.12.33.config
scripts/config --enable   PVM_GUEST
scripts/config --enable   KVM_GUEST
scripts/config --disable  UAPI_HEADER_TEST
scripts/config --set-str  SYSTEM_TRUSTED_KEYS ""
scripts/config --set-str  SYSTEM_REVOCATION_KEYS ""
scripts/config --set-str  LOCALVERSION "-pvmguest"
export ARCH=x86_64 CROSS_COMPILE=x86_64-linux-gnu-
make olddefconfig
make -j"$JOBS" vmlinux
OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"
x86_64-linux-gnu-objcopy --strip-debug vmlinux "$OUT/vmlinux-pvmguest"
file "$OUT/vmlinux-pvmguest"
echo GUEST_KERNEL_BUILD_DONE
