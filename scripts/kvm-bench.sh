#!/bin/bash
set -euo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
BIN=/Users/dylanwong/daedal/bin/daedald-linux-arm64
STATE=/tmp/daedald-kvm
API=http://127.0.0.1:7031
RESULTS=/Users/dylanwong/daedal/bench/results
mkdir -p "$RESULTS"

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$B/vmlinux-aarch64" "$STATE/state/images/vmlinux-kvm"
cp "$B/rootfs.ext4" "$STATE/state/images/rootfs.ext4"
sudo install -m0755 "$B/firecracker-aarch64" /usr/local/bin/firecracker

cat > "$STATE/config.json" <<JSON
{ "listen": "127.0.0.1:7031", "state_dir": "$STATE/state", "firecracker_bin": "/usr/local/bin/firecracker",
  "backends": { "kvm": {
    "kernel_path": "$STATE/state/images/vmlinux-kvm",
    "rootfs_path": "$STATE/state/images/rootfs.ext4",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off init=/init",
    "vcpus": 1, "mem_mib": 128, "track_dirty_pages": true } } }
JSON

"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

SIZES="${1:-128 256 512 1024}"
for MEM in $SIZES; do
  echo "== KVM bench mem=${MEM}MiB precopy n=100 =="
  "$BIN" bench -api "$API" -n 100 -mode precopy -backend kvm -mem "$MEM" \
    -name "kvm-$MEM" -report "$RESULTS/kvm-precopy-${MEM}.json" -target-ms 20000 || echo "FAILED at mem=$MEM"
done

echo "== aggregate metrics =="
curl -s "$API/v1/metrics"
echo
echo KVM_BENCH_DONE
