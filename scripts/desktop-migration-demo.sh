#!/bin/bash
# Wires up the full GUI demo: two persistent XFCE desktop guests (one per
# host, never migrated) each VNC-tunneled to the browser over iroh QUIC, plus
# the fully decoupled beacon-guest live migration path (api/internal/livemigrate,
# unchanged) that the frontend's Migrate button drives over SSE. Unlike every
# other script here, this one is a mixed macOS/VM driver, not a single
# require_linux-forwarded script: daedald (cross-built, no cgo) and
# vnc-ws-gateway run directly on the macOS host, while the Firecracker guests,
# vnc-tunnel-agent (needs to reach the guest's bridge IP directly), and
# daedald livemigrate serve (needs /dev/kvm) run inside the Lima VM via
# explicit `limactl shell`. Lima's vz driver forwards a guest's 127.0.0.1:PORT
# listener to the same macOS 127.0.0.1:PORT automatically (verified live),
# which is why daedald can run in the VM yet still be reachable at the
# frontend's hardcoded http://localhost:7040.
set -uo pipefail
ROOT=/Users/dylanwong/daedal
IMAGES=$ROOT/kernel/build
DAEDAL_VM=${DAEDAL_VM:-daedal-kvm}
BR=${BR:-br0}
HOST_IP=${HOST_IP:-172.20.0.1}
DESK_A_IP=${DESK_A_IP:-172.20.0.3}
DESK_B_IP=${DESK_B_IP:-172.20.0.4}
DESK_A_MAC=${DESK_A_MAC:-06:00:AC:14:00:03}
DESK_B_MAC=${DESK_B_MAC:-06:00:AC:14:00:04}
DESK_A_TAP=${DESK_A_TAP:-tap-desk-a}
DESK_B_TAP=${DESK_B_TAP:-tap-desk-b}
DESK_A_IMG=${DESK_A_IMG:-rootfs-desktop}
DESK_B_IMG=${DESK_B_IMG:-rootfs-desktop-b}
DAEDALD_LISTEN=${DAEDALD_LISTEN:-127.0.0.1:7040}
SHARED_LM=${SHARED_LM:-/dev/shm/daedal-lm}
WEB_PORT=${WEB_PORT:-4173}

vm_run() { limactl shell "$DAEDAL_VM" -- bash -lc "$1"; }

echo "=== preflight ==="
if ! command -v limactl >/dev/null 2>&1; then
  echo "ERROR: limactl not found on macOS host (needed to drive $DAEDAL_VM)" >&2
  exit 1
fi
if ! limactl list 2>/dev/null | awk -v v="$DAEDAL_VM" '$1==v && $2=="Running"{f=1} END{exit !f}'; then
  echo "ERROR: Lima VM '$DAEDAL_VM' is not Running. Start it first:" >&2
  echo "    limactl start $DAEDAL_VM" >&2
  exit 1
fi
for c in go cargo npm; do
  command -v "$c" >/dev/null 2>&1 || { echo "ERROR: $c not found on macOS host" >&2; exit 1; }
done
echo "vm=$DAEDAL_VM running, go/cargo/npm present on macOS host"

echo "=== rebuild daedald for linux/arm64 (macOS cross-compile, no cgo) ==="
mkdir -p "$ROOT/bin"
( cd "$ROOT/api" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ROOT/bin/daedald-linux-arm64" ./cmd/daedald ) \
  && echo "daedald-linux-arm64 rebuilt: $(file -b "$ROOT/bin/daedald-linux-arm64" 2>/dev/null)" \
  || { echo "ERROR: daedald cross-build failed" >&2; exit 1; }

echo "=== VM device prep (kvm, userfaultfd) ==="
vm_run 'sudo chmod 666 /dev/kvm; sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null; sudo chmod 666 /dev/userfaultfd 2>/dev/null || true'

echo "=== start daedald livemigrate serve (decoupled beacon-VM migration path) ==="
vm_run "sudo pkill -f 'bin/daedald-linux-arm64 livemigrate serve' 2>/dev/null || true; sleep 0.3
sudo mkdir -p $SHARED_LM; sudo chmod 777 $SHARED_LM
sudo rm -f /tmp/daedald-serve.log
sudo nohup $ROOT/bin/daedald-linux-arm64 livemigrate serve \
  -listen $DAEDALD_LISTEN -firecracker $IMAGES/firecracker-aarch64 \
  -kernel $IMAGES/vmlinux-aarch64 -rootfs $IMAGES/rootfs-net.ext4 \
  -shared-dir $SHARED_LM -target-ms 30 > /tmp/daedald-serve.log 2>&1 &
disown
sleep 1
tail -5 /tmp/daedald-serve.log"
echo "daedald livemigrate serve started inside $DAEDAL_VM, Lima-forwarded to macOS at http://$DAEDALD_LISTEN"
echo "NOTE: this endpoint spawns its own local Firecracker pair per migration (Config.Attach is never"
echo "set by liveMigrateServe -- see api/cmd/daedald/livemigrate_server.go); it does not attach to,"
echo "or configure a guest NIC on, separately-started container hosts. See final report for the gap."

echo "=== beacon container/networking setup, reused unchanged from two-host-demo.sh ==="
vm_run "sudo chmod 666 /dev/kvm
sudo podman rm -f beacon-host-a beacon-host-b >/dev/null 2>&1 || true
sudo ip link del beacon-br0 2>/dev/null || true
sudo ip link add beacon-br0 type bridge
sudo ip addr add 172.21.0.1/24 dev beacon-br0
sudo ip link set beacon-br0 up
for t in beacon-tap-src beacon-tap-dst; do
  sudo ip link del \"\$t\" 2>/dev/null || true
  sudo ip tuntap add \"\$t\" mode tap; sudo ip link set \"\$t\" master beacon-br0; sudo ip link set \"\$t\" up
done
sudo podman run -d --name beacon-host-a --network host --privileged \
  --device /dev/kvm --device /dev/userfaultfd \
  -v $SHARED_LM:$SHARED_LM -v $IMAGES:$IMAGES:ro \
  --rootfs \$HOME/host-rootfs:O \
  /bin/sh -c 'exec /bin/firecracker --api-sock $SHARED_LM/beacon-a.sock' >/dev/null
sudo podman run -d --name beacon-host-b --network host --privileged \
  --device /dev/kvm --device /dev/userfaultfd \
  -v $SHARED_LM:$SHARED_LM -v $IMAGES:$IMAGES:ro \
  --rootfs \$HOME/host-rootfs:O \
  /bin/sh -c 'exec /bin/firecracker --api-sock $SHARED_LM/beacon-b.sock' >/dev/null
sleep 1
echo \"beacon-host-a=\$(sudo podman inspect -f '{{.State.Running}}' beacon-host-a 2>&1) beacon-host-b=\$(sudo podman inspect -f '{{.State.Running}}' beacon-host-b 2>&1)\""
echo "two idle beacon-host-a/beacon-host-b containers are up (same podman --rootfs :O pattern as"
echo "two-host-demo.sh) so the topology two-host-demo.sh proved is available; they are not yet"
echo "attached to daedald livemigrate serve above (see report: attach-mode is CLI-only today)."

echo "=== desktop guest rootfs images ==="
for pair in "$DESK_A_IMG:$DESK_A_IP" "$DESK_B_IMG:$DESK_B_IP"; do
  img=${pair%%:*}; ip=${pair##*:}
  if vm_run "test -f $IMAGES/$img.ext4"; then
    echo "$img.ext4 already present, skipping debootstrap build"
  else
    echo "$img.ext4 missing -- building via build-desktop-rootfs.sh (debootstrap+apt, several minutes)"
    vm_run "cd $ROOT && GUEST_IP=$ip IMG_NAME=$img DAEDAL_NO_FORWARD=1 bash scripts/build-desktop-rootfs.sh" \
      || echo "WARNING: build-desktop-rootfs.sh failed for $img, host will not boot -- see report"
  fi
done

boot_desktop() { # boot_desktop <tap> <mac> <img> <rundir>
  tap=$1; mac=$2; img=$3; rundir=$4
  if ! vm_run "test -f $IMAGES/$img.ext4"; then
    echo "SKIP: $img.ext4 not present, not booting $rundir"
    return 1
  fi
  vm_run "if ! ip link show $BR >/dev/null 2>&1; then
    sudo ip link add $BR type bridge; sudo ip addr add $HOST_IP/24 dev $BR; sudo ip link set $BR up
  fi
  sudo ip link del $tap 2>/dev/null || true
  sudo ip tuntap add $tap mode tap; sudo ip link set $tap master $BR; sudo ip link set $tap up
  sudo pkill -f \"api-sock $rundir/fc.sock\" 2>/dev/null || true
  sleep 0.3
  sudo rm -rf $rundir; sudo mkdir -p $rundir
  sudo nohup $IMAGES/firecracker-aarch64 --api-sock $rundir/fc.sock > $rundir/serial.log 2>&1 &
  disown
  for i in \$(seq 1 100); do [ -S $rundir/fc.sock ] && break; sleep 0.05; done
  api(){ sudo curl -s --unix-socket $rundir/fc.sock -X \"\$1\" \"http://localhost\$2\" -H 'Content-Type: application/json' -d \"\$3\"; }
  api PUT /boot-source '{\"kernel_image_path\":\"$IMAGES/vmlinux-aarch64\",\"boot_args\":\"console=ttyS0 reboot=k panic=1 pci=off net.ifnames=0 biosdevname=0 root=/dev/vda rw init=/sbin/init\"}'
  api PUT /drives/rootfs '{\"drive_id\":\"rootfs\",\"path_on_host\":\"$IMAGES/$img.ext4\",\"is_root_device\":true,\"is_read_only\":false}'
  api PUT /machine-config '{\"vcpu_count\":2,\"mem_size_mib\":1024}'
  api PUT /network-interfaces/eth0 '{\"iface_id\":\"eth0\",\"guest_mac\":\"$mac\",\"host_dev_name\":\"$tap\"}'
  api PUT /actions '{\"action_type\":\"InstanceStart\"}'
  echo
  echo \"$rundir: api_sock=$rundir/fc.sock\""
}

echo "=== boot desktop host A ($DESK_A_IP, tap $DESK_A_TAP) ==="
if boot_desktop "$DESK_A_TAP" "$DESK_A_MAC" "$DESK_A_IMG" /tmp/fc-desktop-a; then
  echo "desktop host A: boot sequence sent"
else
  echo "desktop host A: not booted (rootfs missing)"
fi
echo "=== boot desktop host B ($DESK_B_IP, tap $DESK_B_TAP) ==="
if boot_desktop "$DESK_B_TAP" "$DESK_B_MAC" "$DESK_B_IMG" /tmp/fc-desktop-b; then
  echo "desktop host B: boot sequence sent"
else
  echo "desktop host B: not booted (rootfs missing)"
fi

start_vnc_agent() { # start_vnc_agent <vnc-addr> <logfile>
  vnc_addr=$1; log=$2
  AGENT_CROSS=$ROOT/gateway/target/aarch64-unknown-linux-gnu/release/vnc-tunnel-agent
  if [ -x "$AGENT_CROSS" ]; then
    echo "using cross-compiled aarch64-unknown-linux-gnu vnc-tunnel-agent"
    vm_run "sudo rm -f $log
    nohup $AGENT_CROSS --vnc-addr $vnc_addr > $log 2>&1 &
    disown
    sleep 1; cat $log"
  else
    echo "no aarch64-unknown-linux-gnu build found, running vnc-tunnel-agent via cargo run inside $DAEDAL_VM"
    vm_run "sudo rm -f $log
    cd $ROOT/gateway
    nohup cargo run --release -p vnc-tunnel-agent -- --vnc-addr $vnc_addr > $log 2>&1 &
    disown
    for i in \$(seq 1 120); do grep -q 'node id' $log 2>/dev/null && break; sleep 1; done
    cat $log"
  fi
}

echo "=== vnc-tunnel-agent for desktop host A (--vnc-addr $DESK_A_IP:5901) ==="
start_vnc_agent "$DESK_A_IP:5901" /tmp/vnc-agent-a.log
NODE_ID_A=$(vm_run "cat /tmp/vnc-agent-a.log 2>/dev/null" | grep -o 'node id: [0-9a-f]\{64\}' | head -1 | awk '{print $NF}')
echo "=== vnc-tunnel-agent for desktop host B (--vnc-addr $DESK_B_IP:5901) ==="
start_vnc_agent "$DESK_B_IP:5901" /tmp/vnc-agent-b.log
NODE_ID_B=$(vm_run "cat /tmp/vnc-agent-b.log 2>/dev/null" | grep -o 'node id: [0-9a-f]\{64\}' | head -1 | awk '{print $NF}')
echo "host A EndpointId: ${NODE_ID_A:-<not printed, see log>}"
echo "host B EndpointId: ${NODE_ID_B:-<not printed, see log>}"

echo "=== register host NodeIds with daedald ==="
if [ -n "${NODE_ID_A:-}" ]; then
  curl -s -X POST "http://$DAEDALD_LISTEN/v1/hosts/host-a/vnc-endpoint" \
    -H 'Content-Type: application/json' -d "{\"node_id\":\"$NODE_ID_A\"}" && echo
else
  echo "SKIP: host-a EndpointId not available, not registering"
fi
if [ -n "${NODE_ID_B:-}" ]; then
  curl -s -X POST "http://$DAEDALD_LISTEN/v1/hosts/host-b/vnc-endpoint" \
    -H 'Content-Type: application/json' -d "{\"node_id\":\"$NODE_ID_B\"}" && echo
else
  echo "SKIP: host-b EndpointId not available, not registering"
fi
echo "registered hosts: $(curl -s "http://$DAEDALD_LISTEN/v1/hosts")"

echo "=== vnc-ws-gateway on macOS host ==="
pkill -f "target/release/vnc-ws-gateway" 2>/dev/null || true
pkill -f "target/debug/vnc-ws-gateway" 2>/dev/null || true
sleep 0.3
GATEWAY_LOG=/tmp/vnc-ws-gateway.log
rm -f "$GATEWAY_LOG"
( cd "$ROOT/gateway" && cargo build --release -p vnc-ws-gateway ) >>"$GATEWAY_LOG" 2>&1
if [ -x "$ROOT/gateway/target/release/vnc-ws-gateway" ]; then
  GATEWAY_BIN="$ROOT/gateway/target/release/vnc-ws-gateway"
elif [ -x "$ROOT/gateway/target/debug/vnc-ws-gateway" ]; then
  echo "release build failed (gateway/ likely mid-edit), falling back to existing debug binary"
  GATEWAY_BIN="$ROOT/gateway/target/debug/vnc-ws-gateway"
else
  GATEWAY_BIN=""
fi
if [ -n "$GATEWAY_BIN" ]; then
  nohup "$GATEWAY_BIN" >"$GATEWAY_LOG" 2>&1 &
  disown
  sleep 1
  GATEWAY_ADDR=$(grep -o 'listening on ws://[^ ]*' "$GATEWAY_LOG" | head -1)
  echo "vnc-ws-gateway: ${GATEWAY_ADDR:-not confirmed, see $GATEWAY_LOG}"
else
  echo "SKIP: no vnc-ws-gateway binary available (see $GATEWAY_LOG); build gateway/ first"
fi

echo "=== frontend ==="
( cd "$ROOT/web" && npm run build ) >/tmp/web-build.log 2>&1 \
  && echo "web/dist rebuilt" \
  || echo "WARNING: web build failed, serving whatever is already in web/dist -- see /tmp/web-build.log"
pkill -f "vite preview" 2>/dev/null || true
sleep 0.3
WEB_LOG=/tmp/web-preview.log
rm -f "$WEB_LOG"
( cd "$ROOT/web" && nohup npm run preview -- --port "$WEB_PORT" --strictPort >"$WEB_LOG" 2>&1 & )
sleep 2
WEB_URL=$(grep -o 'http://localhost:[0-9]*/' "$WEB_LOG" | head -1)

echo
echo "=== demo URL ==="
echo "${WEB_URL:-http://localhost:$WEB_PORT/}"
echo "daedald API:      http://$DAEDALD_LISTEN"
echo "vnc-ws-gateway:    ${GATEWAY_ADDR:-not started}"
echo "host-a EndpointId: ${NODE_ID_A:-none}"
echo "host-b EndpointId: ${NODE_ID_B:-none}"
echo DESKTOP_MIGRATION_DEMO_STARTED
