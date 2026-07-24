#!/bin/bash
# Verify attach mode: the orchestrator drives two independently-started
# Firecracker processes (proxy for two container hosts) via sockets on shared tmpfs.
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
sudo chmod 666 /dev/kvm 2>/dev/null
sudo install -m0755 /Users/dylanwong/daedal/kernel/build/firecracker-aarch64 /usr/local/bin/firecracker
S=/dev/shm/attach; rm -rf "$S"; mkdir -p "$S"
sudo pkill -f 'firecracker --api-sock' 2>/dev/null; sleep 0.5
setsid firecracker --api-sock "$S/src.sock" > "$S/src.log" 2>&1 < /dev/null &
setsid firecracker --api-sock "$S/dst.sock" > "$S/dst.log" 2>&1 < /dev/null &
sleep 1.5
echo "sockets: src=$(test -S $S/src.sock && echo y || echo n) dst=$(test -S $S/dst.sock && echo y || echo n)"
/Users/dylanwong/daedal/bin/daedald-linux-arm64 livemigrate -attach \
  -src-sock "$S/src.sock" -dst-sock "$S/dst.sock" -src-log "$S/src.log" -dst-log "$S/dst.log" \
  -kernel /Users/dylanwong/daedal/kernel/build/vmlinux-aarch64 -rootfs /Users/dylanwong/daedal/kernel/build/rootfs.ext4 \
  -mem 32 -precopy-rounds 3 -mem-backend File -shared-dir "$S" -work-dir /tmp/attach 2>&1
sudo pkill -f 'firecracker --api-sock' 2>/dev/null
echo ATTACH_TEST_DONE
