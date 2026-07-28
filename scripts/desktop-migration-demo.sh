#!/bin/bash
set -uo pipefail
ROOT=/Users/dylanwong/daedal
IMAGES=$ROOT/kernel/build
DAEDAL_VM=${DAEDAL_VM:-daedal-kvm}
BR=${BR:-br0}
HOST_IP=${HOST_IP:-172.20.0.1}
GUEST_IP=${GUEST_IP:-172.20.0.3}
GUEST_MAC=${GUEST_MAC:-06:00:AC:14:00:03}
HOST_A=${HOST_A:-host-a}
HOST_B=${HOST_B:-host-b}
TAP_A=${TAP_A:-tap-desk-a}
TAP_B=${TAP_B:-tap-desk-b}
DESK_IMG=${DESK_IMG:-rootfs-desktop}
GUEST_MEM_MIB=${GUEST_MEM_MIB:-1024}
GUEST_VCPUS=${GUEST_VCPUS:-2}
PRECOPY_ROUNDS=${PRECOPY_ROUNDS:-3}
DAEDALD_LISTEN=${DAEDALD_LISTEN:-127.0.0.1:7040}
SHARED_LM=${SHARED_LM:-/dev/shm/daedal-desk}
SESSION_WORK=${SESSION_WORK:-/tmp/daedal-lm-session}
AGENT_KEYFILE=${AGENT_KEYFILE:-/tmp/agent-desk.key}
AGENT_LOG=${AGENT_LOG:-/tmp/vnc-agent-desk.log}
GATEWAY_TARGET=${GATEWAY_TARGET:-/tmp/gateway-target}
WEB_PORT=${WEB_PORT:-5173}

vm_run() { limactl shell "$DAEDAL_VM" -- bash -lc "$1"; }

echo "=== preflight ==="
command -v limactl >/dev/null 2>&1 || { echo "ERROR: limactl not found on the macOS host" >&2; exit 1; }
if ! limactl list 2>/dev/null | awk -v v="$DAEDAL_VM" '$1==v && $2=="Running"{f=1} END{exit !f}'; then
  echo "ERROR: Lima VM '$DAEDAL_VM' is not Running. Start it with: limactl start $DAEDAL_VM" >&2
  exit 1
fi
for c in go cargo npm; do
  command -v "$c" >/dev/null 2>&1 || { echo "ERROR: $c not found on the macOS host" >&2; exit 1; }
done
vm_run "test -f $IMAGES/$DESK_IMG.ext4" || {
  echo "ERROR: $IMAGES/$DESK_IMG.ext4 is missing. Build it first:" >&2
  echo "    bash scripts/build-desktop-rootfs.sh" >&2
  exit 1
}
echo "vm=$DAEDAL_VM running, go/cargo/npm present, $DESK_IMG.ext4 present"

echo "=== rebuild daedald for linux/arm64 (macOS cross-compile, no cgo) ==="
mkdir -p "$ROOT/bin"
( cd "$ROOT/api" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ROOT/bin/daedald-linux-arm64" ./cmd/daedald ) \
  && echo "daedald-linux-arm64: $(file -b "$ROOT/bin/daedald-linux-arm64" 2>/dev/null)" \
  || { echo "ERROR: daedald cross-build failed" >&2; exit 1; }

echo "=== stop anything already holding the desktop rootfs or port 7040 ==="
vm_run "for p in \$(sudo ss -H -ltnp 'sport = :${DAEDALD_LISTEN##*:}' 2>/dev/null | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u); do
  echo \"killing pid \$p holding port ${DAEDALD_LISTEN##*:} (\$(cat /proc/\$p/comm 2>/dev/null))\"
  sudo kill \$p 2>/dev/null || true
done
if [ -f /tmp/fc-desktop/fc.pid ]; then sudo kill \$(cat /tmp/fc-desktop/fc.pid) 2>/dev/null || true; fi
for p in \$(pgrep -x firecracker 2>/dev/null); do
  if tr '\\0' ' ' < /proc/\$p/cmdline 2>/dev/null | grep -q '$SESSION_WORK'; then
    echo \"killing stale session firecracker pid \$p\"
    sudo kill \$p 2>/dev/null || true
  fi
done
sleep 1
echo stopped"

echo "=== VM device and network prep ==="
vm_run "sudo chmod 666 /dev/kvm
sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null 2>&1 || true
sudo chmod 666 /dev/userfaultfd 2>/dev/null || true
if ! ip link show $BR >/dev/null 2>&1; then
  sudo ip link add $BR type bridge
  sudo ip addr add $HOST_IP/24 dev $BR
  sudo ip link set $BR up
fi
UPLINK=\$(ip route show default | awk '/default/ {print \$5; exit}')
if [ -n \"\$UPLINK\" ] && ! sudo iptables -t nat -C POSTROUTING -s ${HOST_IP%.*}.0/24 -o \"\$UPLINK\" -j MASQUERADE 2>/dev/null; then
  sudo iptables -t nat -A POSTROUTING -s ${HOST_IP%.*}.0/24 -o \"\$UPLINK\" -j MASQUERADE
fi
for t in $TAP_A $TAP_B; do
  sudo ip link del \$t 2>/dev/null || true
  sudo ip tuntap add \$t mode tap user \$(id -un)
  sudo ip link set \$t master $BR
  sudo ip link set \$t up
done
sudo rm -rf $SHARED_LM $SESSION_WORK
mkdir -p $SHARED_LM $SESSION_WORK
chmod 777 $SHARED_LM
ip -br link show | grep -E '$BR|$TAP_A|$TAP_B'
df -h /dev/shm | tail -1"

echo "=== start daedald livemigrate serve -persistent-guest (the desktop guest IS the migrating VM) ==="
vm_run "rm -f /tmp/daedald-serve.log
setsid $ROOT/bin/daedald-linux-arm64 livemigrate serve \
  -listen $DAEDALD_LISTEN \
  -firecracker $IMAGES/firecracker-aarch64 \
  -kernel $IMAGES/vmlinux-aarch64 \
  -rootfs $IMAGES/$DESK_IMG.ext4 \
  -shared-dir $SHARED_LM \
  -session-work-dir $SESSION_WORK \
  -persistent-guest \
  -guest-rootfs $IMAGES/$DESK_IMG.ext4 \
  -guest-mem-mib $GUEST_MEM_MIB \
  -guest-vcpus $GUEST_VCPUS \
  -guest-mac $GUEST_MAC \
  -host-a $HOST_A -host-b $HOST_B \
  -host-a-tap $TAP_A -host-b-tap $TAP_B \
  -precopy-rounds $PRECOPY_ROUNDS \
  -target-ms 30 < /dev/null > /tmp/daedald-serve.log 2>&1 &
sleep 3
cat /tmp/daedald-serve.log"

echo "=== wait for the desktop guest to bring up VNC on $GUEST_IP:5901 ==="
vm_run "for i in \$(seq 1 120); do
  if timeout 2 bash -c 'cat < /dev/null > /dev/tcp/$GUEST_IP/5901' 2>/dev/null; then echo \"VNC_UP after \${i}s\"; break; fi
  sleep 1
done
timeout 3 curl -s http://$DAEDALD_LISTEN/v1/migrations/guest || true"
echo

CURRENT_HOST=$(curl -s "http://$DAEDALD_LISTEN/v1/migrations/current-host" | sed -n 's/.*"host":"\([^"]*\)".*/\1/p')
echo "persistent guest currently on: ${CURRENT_HOST:-unknown}"

echo "=== vnc-tunnel-agent (one agent on the bridge; the guest keeps $GUEST_IP on both hosts) ==="
vm_run "sudo pkill -x vnc-tunnel-agent 2>/dev/null || true
sleep 0.3
rm -f $AGENT_LOG
AGENT=$GATEWAY_TARGET/release/vnc-tunnel-agent
if [ ! -x \"\$AGENT\" ]; then
  ( cd $ROOT/gateway && CARGO_TARGET_DIR=$GATEWAY_TARGET cargo build --release -p vnc-tunnel-agent ) >/tmp/agent-build.log 2>&1
fi
if [ ! -x \"\$AGENT\" ]; then echo 'ERROR: no vnc-tunnel-agent binary, see /tmp/agent-build.log'; exit 1; fi
setsid \$AGENT --keyfile $AGENT_KEYFILE --vnc-addr $GUEST_IP:5901 < /dev/null > $AGENT_LOG 2>&1 &
for i in \$(seq 1 30); do grep -q 'node id' $AGENT_LOG 2>/dev/null && break; sleep 1; done
cat $AGENT_LOG"
NODE_ID=$(vm_run "cat $AGENT_LOG 2>/dev/null" | grep -o 'node id: [0-9a-f]\{64\}' | head -1 | awk '{print $NF}')
echo "vnc-tunnel-agent EndpointId: ${NODE_ID:-<not printed, see $AGENT_LOG>}"

echo "=== register the tunnel node id for both hosts ==="
if [ -n "${NODE_ID:-}" ]; then
  for h in "$HOST_A" "$HOST_B"; do
    curl -s -X POST "http://$DAEDALD_LISTEN/v1/hosts/$h/vnc-endpoint" \
      -H 'Content-Type: application/json' -d "{\"node_id\":\"$NODE_ID\"}" >/dev/null
  done
  echo "registered: $(curl -s "http://$DAEDALD_LISTEN/v1/hosts")"
else
  echo "SKIP: no EndpointId to register"
fi

echo "=== vnc-ws-gateway on the macOS host ==="
GATEWAY_LOG=/tmp/vnc-ws-gateway.log
pkill -x vnc-ws-gateway 2>/dev/null || true
sleep 0.3
rm -f "$GATEWAY_LOG"
( cd "$ROOT/gateway" && cargo build --release -p vnc-ws-gateway ) >>"$GATEWAY_LOG" 2>&1
GATEWAY_BIN=""
for cand in "$ROOT/gateway/target/release/vnc-ws-gateway" "$ROOT/gateway/target/debug/vnc-ws-gateway"; do
  [ -x "$cand" ] && { GATEWAY_BIN=$cand; break; }
done
if [ -n "$GATEWAY_BIN" ]; then
  nohup "$GATEWAY_BIN" >"$GATEWAY_LOG" 2>&1 &
  disown
  sleep 1
  GATEWAY_ADDR=$(grep -o 'listening on ws://[^ ]*' "$GATEWAY_LOG" | head -1)
  echo "vnc-ws-gateway: ${GATEWAY_ADDR:-not confirmed, see $GATEWAY_LOG}"
else
  echo "SKIP: no vnc-ws-gateway binary (see $GATEWAY_LOG)"
fi

echo "=== frontend (vite dev server on :$WEB_PORT) ==="
if curl -s -o /dev/null "http://localhost:$WEB_PORT/"; then
  echo "already serving on :$WEB_PORT, leaving it alone"
else
  ( cd "$ROOT/web" && npm install >/tmp/web-install.log 2>&1 )
  ( cd "$ROOT/web" && nohup npm run dev -- --port "$WEB_PORT" --strictPort >/tmp/web-dev.log 2>&1 & )
  sleep 4
  grep -o 'http://localhost:[0-9]*/' /tmp/web-dev.log | head -1
fi

echo
echo "=== demo URL ==="
echo "http://localhost:$WEB_PORT/?vnc=${NODE_ID:-MISSING}&owner=${CURRENT_HOST:-$HOST_A}"
echo
echo "daedald API:        http://$DAEDALD_LISTEN"
echo "current host:       $(curl -s "http://$DAEDALD_LISTEN/v1/migrations/current-host")"
echo "vnc-ws-gateway:     ${GATEWAY_ADDR:-not started}"
echo "tunnel EndpointId:  ${NODE_ID:-none}"
echo "guest:              $GUEST_IP:5901, ${GUEST_MEM_MIB}MiB, migrating between $HOST_A ($TAP_A) and $HOST_B ($TAP_B)"
echo
echo "Clicking Migrate live-migrates THIS desktop guest between the two hosts; it is never rebooted."
echo DESKTOP_MIGRATION_DEMO_STARTED
