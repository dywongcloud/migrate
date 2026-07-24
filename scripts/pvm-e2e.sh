#!/bin/bash
set -uo pipefail
# Run INSIDE daedal-pvm AFTER rebooting into 6.12.33-pvm and loading kvm-pvm.
# Reads artifacts from ~/pvm-run (staged onto local disk before reboot, so this
# does not depend on the ~/daedal mount). Proves the PVM backend: firecracker on
# PVM's SOFTWARE /dev/kvm boots a microVM and migrates, workload continuity kept.
RUN=~/pvm-run
BIN="$RUN/daedald-linux-amd64"
STATE=/tmp/daedald-pvm
API=http://127.0.0.1:7031

echo "== kernel + PVM module state =="
uname -r
sudo modprobe kvm-pvm 2>&1 || true
lsmod | grep -E 'kvm_pvm|kvm' || true
ls -l /dev/kvm || { echo "NO /dev/kvm — kvm-pvm not loaded"; exit 1; }
echo "-- dmesg PVM / FSGSBASE / RDTSCP lines --"
sudo dmesg | grep -iE 'pvm|fsgsbase|rdtscp' | tail -14 || true

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

echo "== capabilities (expect kvm_pvm_module true, default pvm) =="
curl -s "$API/v1/capabilities"; echo

VM=$(curl -s -X POST "$API/v1/vms" -d '{"name":"pvm-e2e","backend":"pvm","mem_mib":128}' | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))')
echo "vm=$VM"
if [ -z "$VM" ]; then echo "create failed:"; tail -5 "$STATE/daemon.log"; curl -s -X POST "$API/v1/vms" -d '{"name":"x","backend":"pvm","mem_mib":128}'; exit 1; fi
sleep 15
echo "-- console after boot --"
curl -s "$API/v1/vms/$VM/console" | grep -E 'GUEST_BOOTED|HEARTBEAT' | tail -6 || true

echo "== migrate (cold, local) =="
curl -s -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"cold"}' \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print("status",d["status"],"total_ms",d["total_ms"],"downtime_ms",d["downtime_ms"],"error",d.get("error","")[:80])'
sleep 8
curl -s "$API/v1/vms/$VM/console" | grep -E 'GUEST_BOOTED|HEARTBEAT' | tail -8 || true
echo "GUEST_BOOTED_count=$(curl -s "$API/v1/vms/$VM/console" | grep -c GUEST_BOOTED)"

echo "== bench pvm n=30 precopy (TCG-slow; assert p99<20s) =="
"$BIN" bench -api "$API" -n 30 -mode precopy -backend pvm -mem 128 \
  -name pvm-bench -report "$RUN/pvm-precopy-128.json" -target-ms 20000 || echo "BENCH_FAILED"

curl -s -X DELETE "$API/v1/vms/$VM" >/dev/null
echo PVM_E2E_DONE
