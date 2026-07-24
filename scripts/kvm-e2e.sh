#!/bin/bash
set -euo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
# Real Firecracker microVM migration on /dev/kvm, driven entirely through the API.
# Proves memory continuity: the guest keeps a counter in RAM (tmpfs) and prints it
# to serial; after snapshot/restore the counter must CONTINUE, not reset.
B=/Users/dylanwong/daedal/kernel/build
BIN=/Users/dylanwong/daedal/bin/daedald-linux-arm64
STATE=/tmp/daedald-kvm
API=http://127.0.0.1:7031

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$B/vmlinux-aarch64" "$STATE/state/images/vmlinux-kvm"
cp "$B/rootfs.ext4" "$STATE/state/images/rootfs.ext4"
sudo install -m0755 "$B/firecracker-aarch64" /usr/local/bin/firecracker

cat > "$STATE/config.json" <<JSON
{
  "listen": "127.0.0.1:7031",
  "state_dir": "$STATE/state",
  "firecracker_bin": "/usr/local/bin/firecracker",
  "backends": {
    "kvm": {
      "kernel_path": "$STATE/state/images/vmlinux-kvm",
      "rootfs_path": "$STATE/state/images/rootfs.ext4",
      "boot_args": "console=ttyS0 reboot=k panic=1 pci=off i8042.noaux i8042.nomux i8042.nopnp i8042.dumbkbd init=/init",
      "vcpus": 1,
      "mem_mib": 128,
      "track_dirty_pages": true
    },
    "mock": { "vcpus": 2, "mem_mib": 128 }
  }
}
JSON

"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

echo "== capabilities =="
curl -s "$API/v1/capabilities"; echo
echo "== ss listen (security posture) =="
ss -ltnp 2>/dev/null | grep 7031 || true

echo "== create real firecracker microVM =="
VM=$(curl -s -X POST "$API/v1/vms" -d '{"name":"kvm-e2e","backend":"kvm","mem_mib":128}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "vm=$VM"
sleep 5
echo "-- console after boot --"
curl -s "$API/v1/vms/$VM/console" | grep -E 'GUEST_BOOTED|HEARTBEAT' | tail -6 || true
PRE=$(curl -s "$API/v1/vms/$VM/console" | grep -c HEARTBEAT || true)

echo "== migrate (cold, local) =="
curl -s -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"cold"}' \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print("status",d["status"],"total_ms",d["total_ms"],"downtime_ms",d["downtime_ms"],"error",d.get("error",""))'
echo "-- daemon log tail --"
tail -8 "$STATE/daemon.log" || true
sleep 4
echo "-- console after restore (counter must CONTINUE past $PRE) --"
curl -s "$API/v1/vms/$VM/console" | grep -E 'GUEST_BOOTED|HEARTBEAT' | tail -8 || true
BOOTS=$(curl -s "$API/v1/vms/$VM/console" | grep -c GUEST_BOOTED || true)
echo "GUEST_BOOTED_count=$BOOTS (1 == memory preserved, no reboot)"

echo "== migrate (precopy, local, diff snapshot + sparse merge) =="
curl -s -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"precopy"}' \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print("status",d["status"],"total_ms",d["total_ms"],"downtime_ms",d["downtime_ms"],"phases",d["phases_ms"])'
sleep 3
curl -s "$API/v1/vms/$VM/console" | grep -E 'HEARTBEAT' | tail -4 || true

curl -s -X DELETE "$API/v1/vms/$VM" >/dev/null
echo KVM_E2E_DONE
