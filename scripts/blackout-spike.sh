#!/bin/bash
# Mechanism spike: measure pure guest blackout of a UFFD post-copy cutover between
# two Firecracker processes sharing a tmpfs. Guest memory is served to the
# destination lazily over UFFD, so it is NOT loaded during the pause -> the
# blackout is only (pause source + final diff + merge + dest restore/resume).
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
FC=/usr/local/bin/firecracker
UFFD=$(find ~/fc/firecracker-next/build ~/fc/firecracker-next/target -name uffd_on_demand_handler -type f -executable 2>/dev/null | head -1)
MEM_MIB=${1:-128}
SHM=/dev/shm/mig; rm -rf "$SHM"; mkdir -p "$SHM"
RUN=/tmp/blackout; rm -rf "$RUN"; mkdir -p "$RUN"
sudo install -m0755 "$B/firecracker-aarch64" "$FC"
sudo chmod 666 /dev/kvm
sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null 2>&1 || true
[ -e /dev/userfaultfd ] && sudo chmod 666 /dev/userfaultfd

echo "firecracker=$($FC --version | head -1)  uffd_handler=$UFFD  mem=${MEM_MIB}MiB"

api() { # api <sock> <method> <path> <json>
  curl -s --unix-socket "$1" -X "$2" "http://localhost$3" -H 'Content-Type: application/json' -d "$4"
}

# ---- source: boot a guest ----
SRC_SOCK="$RUN/src.sock"; rm -f "$SRC_SOCK"
"$FC" --api-sock "$SRC_SOCK" > "$RUN/src.log" 2>&1 &
SRC_PID=$!
for i in $(seq 1 100); do [ -S "$SRC_SOCK" ] && break; sleep 0.02; done
api "$SRC_SOCK" PUT /boot-source '{"kernel_image_path":"'$B'/vmlinux-aarch64","boot_args":"console=ttyS0 reboot=k panic=1 pci=off init=/init"}' >/dev/null
api "$SRC_SOCK" PUT /drives/rootfs '{"drive_id":"rootfs","path_on_host":"'$B'/rootfs.ext4","is_root_device":true,"is_read_only":false}' >/dev/null
api "$SRC_SOCK" PUT /machine-config '{"vcpu_count":1,"mem_size_mib":'$MEM_MIB',"track_dirty_pages":true}' >/dev/null
api "$SRC_SOCK" PUT /actions '{"action_type":"InstanceStart"}' >/dev/null
sleep 4
echo "source booted; console tail:"; grep -aE 'GUEST_BOOTED|HEARTBEAT' "$RUN/src.log" | tail -2

# ---- pre-copy: full snapshot to shared tmpfs, resume source ----
api "$SRC_SOCK" PATCH /vm '{"state":"Paused"}' >/dev/null
api "$SRC_SOCK" PUT /snapshot/create '{"snapshot_type":"Full","snapshot_path":"'$SHM'/base.vmstate","mem_file_path":"'$SHM'/mem"}' >/dev/null
api "$SRC_SOCK" PATCH /vm '{"state":"Resumed"}' >/dev/null
sleep 2   # guest runs, dirties a little

# ---- pre-start destination firecracker + uffd handler (NOT blackout) ----
DST_SOCK="$RUN/dst.sock"; rm -f "$DST_SOCK"
"$FC" --api-sock "$DST_SOCK" > "$RUN/dst.log" 2>&1 &
DST_PID=$!
for i in $(seq 1 100); do [ -S "$DST_SOCK" ] && break; sleep 0.02; done

# ================= CUTOVER (blackout window) =================
T0=$(date +%s%N)
api "$SRC_SOCK" PATCH /vm '{"state":"Paused"}' >/dev/null
api "$SRC_SOCK" PUT /snapshot/create '{"snapshot_type":"Diff","snapshot_path":"'$SHM'/diff.vmstate","mem_file_path":"'$SHM'/diff.mem"}' >/dev/null
# merge diff onto base memfile (sparse copy of dirty extents)
dd if="$SHM/diff.mem" of="$SHM/mem" conv=notrunc,sparse bs=1M 2>/dev/null
# start uffd handler serving the merged memfile
UFFD_SOCK="$SHM/uffd.sock"; rm -f "$UFFD_SOCK"
"$UFFD" "$UFFD_SOCK" "$SHM/mem" > "$RUN/uffd.log" 2>&1 &
UFFD_PID=$!
for i in $(seq 1 200); do [ -S "$UFFD_SOCK" ] && break; sleep 0.001; done
# dest: load snapshot with UFFD memory backend + resume (memory NOT loaded now)
api "$DST_SOCK" PUT /snapshot/load '{"snapshot_path":"'$SHM'/diff.vmstate","mem_backend":{"backend_type":"Uffd","backend_path":"'$UFFD_SOCK'"},"enable_diff_snapshots":false,"resume_vm":true}' >/dev/null
T1=$(date +%s%N)
# ============================================================

BLACKOUT_MS=$(( (T1 - T0) / 1000000 ))
BLACKOUT_US=$(( (T1 - T0) / 1000 ))
echo "=========================================="
echo "BLACKOUT: ${BLACKOUT_MS} ms  (${BLACKOUT_US} us)"
echo "=========================================="
sleep 3
echo "dest console tail (guest must have CONTINUED, not rebooted):"
grep -aE 'GUEST_BOOTED|HEARTBEAT' "$RUN/dst.log" | tail -4
echo "dest GUEST_BOOTED count (1 == migrated, not rebooted): $(grep -ac GUEST_BOOTED "$RUN/dst.log")"

kill $SRC_PID $DST_PID $UFFD_PID 2>/dev/null
echo SPIKE_DONE
