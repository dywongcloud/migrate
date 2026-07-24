#!/bin/bash
set -euxo pipefail
# Run INSIDE daedal-pvm AFTER scripts/stage-host-modules.sh has run in daedal-kvm.
# Installs the cross-compiled PVM host kernel + modules (staged on the shared
# ~/daedal mount), rebuilds initramfs, points GRUB at it with pti=off, reboots.
STAGE=/Users/dylanwong/daedal/kernel/build/pvm-host-install
B=/Users/dylanwong/daedal/kernel/build
KREL=$(cat "$STAGE/kernel.release")

# Stage everything the post-reboot PVM e2e needs onto LOCAL disk, so it does not
# depend on the ~/daedal mount surviving the kernel switch.
RUN=~/pvm-run
mkdir -p "$RUN"
cp "$B/vmlinux-pvmguest" "$B/rootfs.ext4" "$B/firecracker-x86_64" "$RUN/"
cp /Users/dylanwong/daedal/bin/daedald-linux-amd64 "$RUN/"
cp /Users/dylanwong/daedal/scripts/pvm-e2e.sh "$RUN/"

sudo cp -a "$STAGE/lib/modules/$KREL" /lib/modules/
sudo cp "$STAGE/boot/vmlinuz-$KREL" /boot/
sudo cp "$STAGE/boot/System.map-$KREL" /boot/
sudo cp "$STAGE/boot/config-$KREL" /boot/
sudo depmod -a "$KREL"

sudo update-initramfs -c -k "$KREL"

echo "kvm-pvm" | sudo tee /etc/modules-load.d/kvm-pvm.conf
printf 'blacklist kvm_amd\nblacklist kvm_intel\n' | sudo tee /etc/modprobe.d/blacklist-kvm-vendor.conf

# pti=off (PVM is incompatible with page-table isolation).
sudo sed -i 's/\(GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\)"/\1 pti=off"/' /etc/default/grub || true
sudo update-grub
MENU=$(grep -oP "(?<=menuentry ')[^']*$KREL[^']*(?=')" /boot/grub/grub.cfg | head -1 || true)
if [ -n "$MENU" ]; then
  sudo grub-reboot "Advanced options for Ubuntu>$MENU"
  echo "one-shot boot set to: $MENU"
fi
echo "PVM_KERNEL_INSTALLED $KREL -- reboot to boot it"
