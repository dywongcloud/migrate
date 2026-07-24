#!/bin/bash
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
RUN=~/pvm-run
BIN="$RUN/daedald-linux-amd64"
STATE=/tmp/daedald-pvmbench
API=http://127.0.0.1:7031
N=${1:-40}
MODE=${2:-precopy}

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$RUN/vmlinux-pvmguest" "$STATE/state/images/vmlinux-pvm"
cp "$RUN/rootfs.ext4" "$STATE/state/images/rootfs.ext4"
sudo install -m0755 "$RUN/firecracker-x86_64" /usr/local/bin/firecracker

cat > "$STATE/config.json" <<JSON
{ "listen": "127.0.0.1:7031", "state_dir": "$STATE/state", "firecracker_bin": "/usr/local/bin/firecracker",
  "backends": { "pvm": {
    "kernel_path": "$STATE/state/images/vmlinux-pvm",
    "rootfs_path": "$STATE/state/images/rootfs.ext4",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off init=/init",
    "vcpus": 1, "mem_mib": 128, "track_dirty_pages": true } } }
JSON

"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

"$BIN" bench -api "$API" -n "$N" -mode "$MODE" -backend pvm -mem 128 \
  -name "pvm-$MODE" -report "$RUN/pvm-$MODE-128.json" -target-ms 20000
echo PVM_BENCH_DONE
