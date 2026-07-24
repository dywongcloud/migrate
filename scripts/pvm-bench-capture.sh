#!/bin/bash
set -uo pipefail
RUN=~/pvm-run
BIN="$RUN/daedald-linux-amd64"
STATE=/tmp/daedald-pvmcap
API=http://127.0.0.1:7036
N=${1:-40}

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
rm -rf "$STATE"; mkdir -p "$STATE/state/images"
cp "$RUN/vmlinux-pvmguest" "$STATE/state/images/k"
cp "$RUN/rootfs.ext4" "$STATE/state/images/r"
sudo install -m0755 "$RUN/firecracker-x86_64" /usr/local/bin/firecracker
printf '%s' "{\"listen\":\"127.0.0.1:7036\",\"state_dir\":\"$STATE/state\",\"firecracker_bin\":\"/usr/local/bin/firecracker\",\"backends\":{\"pvm\":{\"kernel_path\":\"$STATE/state/images/k\",\"rootfs_path\":\"$STATE/state/images/r\",\"boot_args\":\"console=ttyS0 reboot=k panic=1 pci=off init=/init\",\"vcpus\":1,\"mem_mib\":128,\"track_dirty_pages\":true}}}" > "$STATE/config.json"
"$BIN" serve --config "$STATE/config.json" > "$STATE/daemon.log" 2>&1 &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf "$API/healthz" >/dev/null && break; sleep 0.1; done

VM=$(curl -s -X POST "$API/v1/vms" -d '{"name":"cap","backend":"pvm","mem_mib":128}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
sleep 10
ok=0
for i in $(seq 1 "$N"); do
  R=$(curl -s -X POST "$API/v1/vms/$VM/migrate" -d '{"target":"local","mode":"precopy"}')
  ST=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])' 2>/dev/null || echo parseerr)
  if [ "$ST" = "succeeded" ]; then
    ok=$((ok+1))
  else
    echo "FIRST FAILURE at migration $i (after $ok ok):"
    echo "$R" | head -c 300; echo
    echo "--- firecracker console.log tail ---"
    cat "$STATE/state/vms/$VM"/console.log 2>/dev/null | tail -20
    break
  fi
done
echo "consecutive_ok=$ok"
curl -s -X DELETE "$API/v1/vms/$VM" >/dev/null 2>&1 || true
echo PVM_CAPTURE_DONE
