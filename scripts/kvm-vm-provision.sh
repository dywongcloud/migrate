#!/bin/bash
set -euxo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
mkdir -p ~/pvm ~/fc
if [ ! -d ~/pvm/host-linux/.git ]; then
  git clone --depth 1 --branch pvm-612 https://github.com/virt-pvm/linux ~/pvm/host-linux
fi
if [ ! -d ~/pvm/guest-linux/.git ]; then
  cp -a ~/pvm/host-linux ~/pvm/guest-linux
fi
curl -fsSL -o ~/pvm/pvm-guest.config https://raw.githubusercontent.com/virt-pvm/misc/main/pvm-guest-6.12.33.config
if [ ! -d ~/fc/firecracker-next/.git ]; then
  git clone --depth 1 https://github.com/DecOperations/firecracker-next ~/fc/firecracker-next
fi
if [ ! -x ~/.cargo/bin/cargo ]; then
  curl -sSf --proto '=https' --tlsv1.2 https://sh.rustup.rs -o /tmp/rustup-init.sh
  sh /tmp/rustup-init.sh -y --default-toolchain stable --profile minimal
fi
echo PROVISION_DONE
