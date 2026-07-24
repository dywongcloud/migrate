#!/bin/bash
# Correctness check: does a UFFD-backed restored guest CONTINUE executing?
# Uses a Full snapshot (no diff/merge) to isolate the UFFD-resume behaviour.
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
FC=/usr/local/bin/firecracker
UFFD=$(find ~/fc/firecracker-next/build -name uffd_on_demand_handler -type f -executable 2>/dev/null | head -1)
SHM=/dev/shm/mig2; rm -rf "$SHM"; mkdir -p "$SHM"
RUN=/tmp/uffdok; rm -rf "$RUN"; mkdir -p "$RUN"
sudo install -m0755 "$B/firecracker-aarch64" "$FC" 2>/dev/null
sudo chmod 666 /dev/kvm; sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null; sudo chmod 666 /dev/userfaultfd
api(){ curl -s --unix-socket "$1" -X "$2" "http://localhost$3" -H 'Content-Type: application/json' -d "$4"; }

SRC="$RUN/src.sock"; "$FC" --api-sock "$SRC" > "$RUN/src.log" 2>&1 & SRC_PID=$!
for i in $(seq 1 100); do [ -S "$SRC" ] && break; sleep 0.02; done
api "$SRC" PUT /boot-source '{"kernel_image_path":"'$B'/vmlinux-aarch64","boot_args":"console=ttyS0 reboot=k panic=1 pci=off init=/init"}' >/dev/null
api "$SRC" PUT /drives/rootfs '{"drive_id":"rootfs","path_on_host":"'$B'/rootfs.ext4","is_root_device":true,"is_read_only":false}' >/dev/null
api "$SRC" PUT /machine-config '{"vcpu_count":1,"mem_size_mib":128}' >/dev/null
api "$SRC" PUT /actions '{"action_type":"InstanceStart"}' >/dev/null
sleep 5
echo "source last heartbeat: $(grep -a HEARTBEAT "$RUN/src.log" | tail -1)"

# Full snapshot (complete memfile, no merge needed)
api "$SRC" PATCH /vm '{"state":"Paused"}' >/dev/null
api "$SRC" PUT /snapshot/create '{"snapshot_type":"Full","snapshot_path":"'$SHM'/state","mem_file_path":"'$SHM'/mem"}' >/dev/null
LAST_SRC=$(grep -a HEARTBEAT "$RUN/src.log" | tail -1 | grep -oE '[0-9]+')
echo "snapshot taken at HEARTBEAT $LAST_SRC"

# UFFD handler + dest load
"$UFFD" "$SHM/uffd.sock" "$SHM/mem" > "$RUN/uffd.log" 2>&1 & UFFD_PID=$!
for i in $(seq 1 200); do [ -S "$SHM/uffd.sock" ] && break; sleep 0.001; done
DST="$RUN/dst.sock"; "$FC" --api-sock "$DST" > "$RUN/dst.log" 2>&1 & DST_PID=$!
for i in $(seq 1 100); do [ -S "$DST" ] && break; sleep 0.02; done
api "$DST" PUT /snapshot/load '{"snapshot_path":"'$SHM'/state","mem_backend":{"backend_type":"Uffd","backend_path":"'$SHM'/uffd.sock"},"resume_vm":true}' >/dev/null

echo "watching dest for HEARTBEAT continuation past $LAST_SRC (10s)..."
sleep 10
echo "=== dest HEARTBEATs ==="; grep -a HEARTBEAT "$RUN/dst.log" | tail -8
echo "dest heartbeat count: $(grep -ac HEARTBEAT "$RUN/dst.log")"
echo "dest GUEST_BOOTED (should be 0 = no reboot): $(grep -ac GUEST_BOOTED "$RUN/dst.log")"
echo "uffd handler alive? $(kill -0 $UFFD_PID 2>/dev/null && echo yes || echo no); dest fc alive? $(kill -0 $DST_PID 2>/dev/null && echo yes || echo no)"
kill $SRC_PID $DST_PID $UFFD_PID 2>/dev/null
echo UFFD_OK_DONE
