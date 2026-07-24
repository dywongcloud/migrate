#!/bin/bash
# Networked live-migration demo on one host: a guest runs a UDP beacon; it is
# live-migrated between two Firecracker processes whose taps share a bridge, so
# the guest keeps its IP/MAC. A collector measures the beacon gap = the
# user-visible blackout, independently of the control-plane timing.
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
BIN=/Users/dylanwong/daedal/bin/daedald-linux-arm64
UFFD=$(find ~/fc/firecracker-next/build -name uffd_on_demand_handler -type f -executable | head -1)
BR=br0; SRC_TAP=tap-src; DST_TAP=tap-dst
HOST_IP=172.20.0.1; PORT=9999

sudo chmod 666 /dev/kvm; sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null; sudo chmod 666 /dev/userfaultfd
sudo install -m0755 "$B/firecracker-aarch64" /usr/local/bin/firecracker
sudo pkill -f 'firecracker --api-sock' 2>/dev/null; sudo pkill -f uffd_on_demand 2>/dev/null; sudo pkill -f collector.py 2>/dev/null

# --- bridge + two taps ---
sudo ip link del "$BR" 2>/dev/null || true
sudo ip link add "$BR" type bridge
sudo ip addr add "$HOST_IP/24" dev "$BR"
sudo ip link set "$BR" up
for t in "$SRC_TAP" "$DST_TAP"; do
  sudo ip link del "$t" 2>/dev/null || true
  sudo ip tuntap add "$t" mode tap
  sudo ip link set "$t" master "$BR"
  sudo ip link set "$t" up
done

# --- collector logs every beacon packet with its arrival time ---
python3 /Users/dylanwong/daedal/images/collector.py "$HOST_IP" "$PORT" 22 /tmp/beacon-log.txt > /dev/null 2>&1 &
COL_PID=$!
sleep 0.5

# --- live migration with the networked guest ---
"$BIN" livemigrate -firecracker /usr/local/bin/firecracker -uffd-handler "$UFFD" \
  -kernel "$B/vmlinux-aarch64" -rootfs "$B/rootfs-net.ext4" \
  -mem 32 -precopy-rounds 3 -mem-backend File -target-ms 30 \
  -net-iface eth0 -guest-mac 06:00:AC:14:00:02 -src-tap "$SRC_TAP" -dst-tap "$DST_TAP" \
  -shared-dir /dev/shm/netdemo -work-dir /tmp/netdemo -report /tmp/mig-report.json 2>&1 | tail -1

wait $COL_PID
echo "=== client-observed migration blackout (beacon gap bracketing the cutover) ==="
python3 /Users/dylanwong/daedal/images/analyze.py /tmp/beacon-log.txt /tmp/mig-report.json
sudo pkill -f 'firecracker --api-sock' 2>/dev/null; sudo pkill -f uffd_on_demand 2>/dev/null
echo NET_DEMO_DONE
