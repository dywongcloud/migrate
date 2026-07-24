#!/bin/bash
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
BIN=/Users/dylanwong/daedal/bin/daedald-linux-arm64
STATE=/tmp/daedald-dead
API=http://127.0.0.1:7033

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$B/vmlinux-aarch64" "$STATE/state/images/vmlinux-kvm"
cp "$B/rootfs.ext4" "$STATE/state/images/rootfs.ext4"
sudo install -m0755 "$B/firecracker-aarch64" /usr/local/bin/firecracker
cat > "$STATE/config.json" <<JSON
{ "listen": "127.0.0.1:7033", "state_dir": "$STATE/state", "firecracker_bin": "/usr/local/bin/firecracker",
  "backends": { "kvm": { "kernel_path": "$STATE/state/images/vmlinux-kvm", "rootfs_path": "$STATE/state/images/rootfs.ext4",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off init=/init", "vcpus": 1, "mem_mib": 128, "track_dirty_pages": true } } }
JSON
"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

VM=$(curl -s -X POST "$API/v1/vms" -d '{"name":"dead","backend":"kvm","mem_mib":128}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
sleep 3
PID=$(curl -s "$API/v1/vms/$VM" | python3 -c 'import sys,json;print(json.load(sys.stdin)["pid"])')
echo "vm=$VM firecracker_pid=$PID state_before=$(curl -s "$API/v1/vms/$VM" | python3 -c 'import sys,json;print(json.load(sys.stdin)["state"])')"

echo "-- simulating firecracker death (kill -9 $PID) --"
kill -9 "$PID" 2>/dev/null; sleep 1

echo "-- migrate on dead instance (expect failure + state=error) --"
curl -s -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"cold"}' \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print("mig_status",d["status"],"error",d.get("error","")[:70])'
echo "state_after=$(curl -s "$API/v1/vms/$VM" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["state"],"|",d.get("last_error","")[:60])')"

echo "-- second migrate (must be rejected, not looped) --"
curl -s -o /dev/null -w 'http_status=%{http_code}\n' -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"cold"}'
curl -s -X DELETE "$API/v1/vms/$VM" >/dev/null
echo DEAD_INSTANCE_TEST_DONE
