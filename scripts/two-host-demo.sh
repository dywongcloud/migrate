#!/bin/bash
# Two concurrent container "hosts" running Firecracker, with a guest live-migrated
# from host-a to host-b. Both containers share a tmpfs (for the memory image and
# API sockets) and the host network (so the guest keeps its IP/MAC across the two
# hosts). A collector measures the client-observed blackout; the orchestrator
# measures the control-plane cutover.
set -uo pipefail
ROOTFS=$HOME/host-rootfs
IMAGES=/Users/dylanwong/daedal/kernel/build
SHARED=/dev/shm/dhost
BR=br0; SRC_TAP=tap-src; DST_TAP=tap-dst
HOST_IP=172.20.0.1; PORT=9999
PODMAN="sudo podman"

sudo chmod 666 /dev/kvm; sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null; sudo chmod 666 /dev/userfaultfd
$PODMAN rm -f host-a host-b >/dev/null 2>&1 || true
rm -rf "$SHARED"; mkdir -p "$SHARED"; chmod 777 "$SHARED"

# bridge + two taps (host netns, shared by both --network host containers)
sudo ip link del "$BR" 2>/dev/null || true
sudo ip link add "$BR" type bridge; sudo ip addr add "$HOST_IP/24" dev "$BR"; sudo ip link set "$BR" up
for t in "$SRC_TAP" "$DST_TAP"; do
  sudo ip link del "$t" 2>/dev/null || true
  sudo ip tuntap add "$t" mode tap; sudo ip link set "$t" master "$BR"; sudo ip link set "$t" up
done

# Mount the shared dir and images at IDENTICAL paths inside the container so the
# orchestrator (host) and the container's Firecracker resolve the memfile, sockets
# and guest images to the same files.
run_host() { # run_host <name> <sock> <logbase>
  $PODMAN run -d --name "$1" --network host --privileged \
    --device /dev/kvm --device /dev/userfaultfd \
    -v "$SHARED:$SHARED" -v "$IMAGES:$IMAGES:ro" \
    --rootfs "$ROOTFS" \
    /bin/sh -c "exec /bin/firecracker --api-sock $SHARED/$2 > $SHARED/$3.log 2>&1" >/dev/null
}
run_host host-a src.sock src
run_host host-b dst.sock dst
sleep 2
echo "hosts: a=$($PODMAN inspect -f '{{.State.Running}}' host-a) b=$($PODMAN inspect -f '{{.State.Running}}' host-b)"
echo "sockets: src=$(test -S $SHARED/src.sock && echo y || echo n) dst=$(test -S $SHARED/dst.sock && echo y || echo n)"

python3 /Users/dylanwong/daedal/images/collector.py "$HOST_IP" "$PORT" 22 /tmp/beacon-log.txt >/dev/null 2>&1 &
COL=$!; sleep 0.5

# orchestrator drives the two container hosts; guest paths are container-internal (/images)
sudo /Users/dylanwong/daedal/bin/daedald-linux-arm64 livemigrate -attach \
  -src-sock "$SHARED/src.sock" -dst-sock "$SHARED/dst.sock" \
  -src-log "$SHARED/src.log" -dst-log "$SHARED/dst.log" \
  -kernel "$IMAGES/vmlinux-aarch64" -rootfs "$IMAGES/rootfs-net.ext4" \
  -mem 32 -precopy-rounds 3 -mem-backend File -target-ms 30 \
  -net-iface eth0 -guest-mac 06:00:AC:14:00:02 -src-tap "$SRC_TAP" -dst-tap "$DST_TAP" \
  -shared-dir "$SHARED" -work-dir /tmp/twohost -report /tmp/mig-report.json 2>&1 | tail -1

wait $COL
echo "=== client-observed migration blackout across the two container hosts ==="
python3 /Users/dylanwong/daedal/images/analyze.py /tmp/beacon-log.txt /tmp/mig-report.json 2>&1 || true
echo "=== src firecracker log tail ==="; tail -8 "$SHARED/src.log" 2>&1
echo "=== dst firecracker log tail ==="; tail -6 "$SHARED/dst.log" 2>&1
${KEEP:-$PODMAN rm -f host-a host-b >/dev/null 2>&1} || true
echo TWO_HOST_DEMO_DONE
