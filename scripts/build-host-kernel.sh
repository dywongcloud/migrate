#!/bin/bash
set -euxo pipefail
SRC=~/pvm/host-linux
PATCHES=/Users/dylanwong/daedal/kernel/patches
CFG=/Users/dylanwong/daedal/kernel/config-ubuntu-noble-x86_64
JOBS=$(nproc)

cd "$SRC"
git checkout -- . 2>/dev/null || true
for p in "$PATCHES"/000[1-5]-*.patch; do
  if git apply --check "$p" 2>/dev/null; then
    git apply "$p"
    echo "APPLIED $(basename "$p")"
  else
    echo "SKIP(already?) $(basename "$p")"
  fi
done

cp "$CFG" .config
scripts/config --module   KVM_PVM
scripts/config --module   KVM
scripts/config --disable  KVM_INTEL
scripts/config --disable  KVM_AMD
scripts/config --set-str  SYSTEM_TRUSTED_KEYS ""
scripts/config --set-str  SYSTEM_REVOCATION_KEYS ""
scripts/config --disable  MODULE_SIG_FORCE
scripts/config --disable  DEBUG_INFO_BTF
scripts/config --disable  DEBUG_INFO_BTF_MODULES
scripts/config --enable   DEBUG_INFO_NONE
scripts/config --set-str  LOCALVERSION "-pvm"
scripts/config --disable  LOCALVERSION_AUTO
scripts/config --disable  UAPI_HEADER_TEST
export ARCH=x86_64 CROSS_COMPILE=x86_64-linux-gnu-
make olddefconfig
grep -E 'CONFIG_KVM_PVM|CONFIG_KVM=' .config
make -j"$JOBS" bzImage modules
OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"
cp arch/x86/boot/bzImage "$OUT/bzImage-pvm"
find . -name 'kvm-pvm.ko' -exec cp {} "$OUT/" \;
cat include/config/kernel.release > "$OUT/kernel.release"
file "$OUT/bzImage-pvm"
ls -la "$OUT"
echo HOST_KERNEL_BUILD_DONE
