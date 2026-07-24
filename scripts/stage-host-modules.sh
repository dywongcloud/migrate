#!/bin/bash
set -euxo pipefail
# Run INSIDE daedal-kvm AFTER build-host-kernel.sh. The kernel is cross-compiled
# here; stage its modules + boot artifacts onto the shared ~/daedal mount so
# install-pvm-kernel.sh (running in daedal-pvm) can install them without a rebuild.
SRC=~/pvm/host-linux
STAGE=/Users/dylanwong/daedal/kernel/build/pvm-host-install
KREL=$(cat "$SRC/include/config/kernel.release")

rm -rf "$STAGE"; mkdir -p "$STAGE/boot"
make -C "$SRC" ARCH=x86_64 CROSS_COMPILE=x86_64-linux-gnu- INSTALL_MOD_PATH="$STAGE" modules_install
cp "$SRC/arch/x86/boot/bzImage" "$STAGE/boot/vmlinuz-$KREL"
cp "$SRC/System.map" "$STAGE/boot/System.map-$KREL"
cp "$SRC/.config" "$STAGE/boot/config-$KREL"
echo "$KREL" > "$STAGE/kernel.release"
du -sh "$STAGE/lib/modules/$KREL"
echo "STAGE_DONE $KREL"
