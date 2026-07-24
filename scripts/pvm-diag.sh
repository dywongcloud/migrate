#!/bin/bash
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
RUN=~/pvm-run
BIN="$RUN/daedald-linux-amd64"
STATE=/tmp/daedald-pvmdiag
API=http://127.0.0.1:7034

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$RUN/vmlinux-pvmguest" "$STATE/state/images/vmlinux-pvm"
cp "$RUN/rootfs.ext4" "$STATE/state/images/rootfs.ext4"
sudo install -m0755 "$RUN/firecracker-x86_64" /usr/local/bin/firecracker
cat > "$STATE/config.json" <<JSON
{ "listen": "127.0.0.1:7034", "state_dir": "$STATE/state", "firecracker_bin": "/usr/local/bin/firecracker",
  "backends": { "pvm": { "kernel_path": "$STATE/state/images/vmlinux-pvm", "rootfs_path": "$STATE/state/images/rootfs.ext4",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off init=/init", "vcpus": 1, "mem_mib": 128, "track_dirty_pages": true } } }
JSON
"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

VM=$(curl -s -X POST "$API/v1/vms" -d '{"name":"diag","backend":"pvm","mem_mib":128}' | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))')
echo "vm=$VM"
# Wait longer for TCG+PVM double-emulated boot
for t in 5 10 20 30; do
  sleep $((t == 5 ? 5 : 10))
  BOOTED=$(curl -s "$API/v1/vms/$VM/console" | grep -c GUEST_BOOTED || true)
  HB=$(curl -s "$API/v1/vms/$VM/console" | grep -c HEARTBEAT || true)
  echo "t=${t}s: GUEST_BOOTED=$BOOTED HEARTBEAT=$HB"
  [ "$BOOTED" -ge 1 ] && break
done
echo "=== full guest console (last 40 lines) ==="
curl -s "$API/v1/vms/$VM/console" | tail -40
curl -s -X DELETE "$API/v1/vms/$VM" >/dev/null
echo PVM_DIAG_DONE
