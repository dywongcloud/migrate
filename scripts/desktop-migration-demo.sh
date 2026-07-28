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
TAP_A=${TAP_A:-tap-lm-a}
TAP_B=${TAP_B:-tap-lm-b}
DESK_IMG=${DESK_IMG:-rootfs-desktop}
GUEST_MEM_MIB=${GUEST_MEM_MIB:-1024}
GUEST_VCPUS=${GUEST_VCPUS:-2}
PRECOPY_ROUNDS=${PRECOPY_ROUNDS:-3}
PIN_IP=${PIN_IP:-172.20.0.4}
PIN_MAC=${PIN_MAC:-06:00:AC:14:00:04}
PIN_TAP=${PIN_TAP:-tap-lm-pin}
PIN_IMG=${PIN_IMG:-rootfs-desktop-b}
PIN_RUN=${PIN_RUN:-/tmp/fc-desktop-pin}
DAEDALD_LISTEN=${DAEDALD_LISTEN:-127.0.0.1:7040}
SHARED_LM=${SHARED_LM:-/dev/shm/daedal-desk}
SESSION_WORK=${SESSION_WORK:-/tmp/daedal-lm-session}
AGENT_KEY_MIG=${AGENT_KEY_MIG:-/tmp/agent-desk-mig.key}
AGENT_LOG_MIG=${AGENT_LOG_MIG:-/tmp/vnc-agent-mig.log}
AGENT_KEY_PIN=${AGENT_KEY_PIN:-/tmp/agent-desk-pin.key}
AGENT_LOG_PIN=${AGENT_LOG_PIN:-/tmp/vnc-agent-pin.log}
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
if ! vm_run "test -f $IMAGES/$PIN_IMG.ext4"; then
  echo "$PIN_IMG.ext4 missing -- building the second desktop image ($PIN_IP)"
  vm_run "cd $ROOT && GUEST_IP=$PIN_IP IMG_NAME=$PIN_IMG GUEST_HOSTNAME=daedal-desktop-b DAEDAL_NO_FORWARD=1 bash scripts/build-desktop-rootfs.sh" \
    || { echo "ERROR: could not build $PIN_IMG.ext4" >&2; exit 1; }
fi
echo "vm=$DAEDAL_VM running, go/cargo/npm present, $DESK_IMG.ext4 and $PIN_IMG.ext4 present"

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
if [ -f $PIN_RUN/fc.pid ]; then sudo kill \$(cat $PIN_RUN/fc.pid) 2>/dev/null || true; fi
for d in /proc/[0-9]*; do
  comm=\$(cat \$d/comm 2>/dev/null) || continue
  case \"\$comm\" in firecracker*) ;; *) continue ;; esac
  case \"\$(tr '\\0' ' ' < \$d/cmdline 2>/dev/null)\" in
    *$SESSION_WORK*)
      echo \"killing stale session firecracker pid \${d#/proc/} (\$comm)\"
      sudo kill \${d#/proc/} 2>/dev/null || true
      ;;
  esac
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
sudo pkill -x vnc-tunnel-agent 2>/dev/null || true
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

echo "=== boot the PINNED second desktop guest ($PIN_IP, tap $PIN_TAP, never migrated) ==="
vm_run "sudo e2fsck -fy $IMAGES/$PIN_IMG.ext4 2>&1 | tail -3"
vm_run "cd $ROOT && DAEDAL_NO_FORWARD=1 BR=$BR TAP=$PIN_TAP HOST_IP=$HOST_IP GUEST_MAC=$PIN_MAC RUN=$PIN_RUN ROOTFS=$IMAGES/$PIN_IMG.ext4 bash scripts/boot-desktop.sh 2>&1 | tail -3"

echo "=== wait for both desktops to serve VNC ==="
vm_run "banner(){ timeout 3 bash -c \"exec 3<>/dev/tcp/\\\$1/5901; head -c 12 <&3\" 2>/dev/null; }
for i in \$(seq 1 150); do
  a=\$(banner $GUEST_IP); b=\$(banner $PIN_IP)
  if [ -n \"\$a\" ] && [ -n \"\$b\" ]; then echo \"BOTH_VNC_UP after \${i}s: migrating=[\$a] pinned=[\$b]\"; break; fi
  sleep 1
done
echo \"migrating $GUEST_IP:5901 banner=[\$(banner $GUEST_IP)] pinned $PIN_IP:5901 banner=[\$(banner $PIN_IP)]\""

echo "=== two vnc-tunnel-agents, one per desktop, persistent keyfiles ==="
vm_run "AGENT=$GATEWAY_TARGET/release/vnc-tunnel-agent
if [ ! -x \"\$AGENT\" ]; then
  ( cd $ROOT/gateway && CARGO_TARGET_DIR=$GATEWAY_TARGET cargo build --release -p vnc-tunnel-agent ) >/tmp/agent-build.log 2>&1
fi
if [ ! -x \"\$AGENT\" ]; then echo 'ERROR: no vnc-tunnel-agent binary, see /tmp/agent-build.log'; exit 1; fi
rm -f $AGENT_LOG_MIG $AGENT_LOG_PIN
setsid \$AGENT --keyfile $AGENT_KEY_MIG --vnc-addr $GUEST_IP:5901 < /dev/null > $AGENT_LOG_MIG 2>&1 &
setsid \$AGENT --keyfile $AGENT_KEY_PIN --vnc-addr $PIN_IP:5901 < /dev/null > $AGENT_LOG_PIN 2>&1 &
for i in \$(seq 1 40); do
  grep -q 'node id' $AGENT_LOG_MIG 2>/dev/null && grep -q 'node id' $AGENT_LOG_PIN 2>/dev/null && break
  sleep 1
done
echo \"migrating-desktop agent: \$(cat $AGENT_LOG_MIG)\"
echo \"pinned-desktop agent:    \$(cat $AGENT_LOG_PIN)\""
NODE_ID_MIG=$(vm_run "cat $AGENT_LOG_MIG 2>/dev/null" | grep -o 'node id: [0-9a-f]\{64\}' | head -1 | awk '{print $NF}')
NODE_ID_PIN=$(vm_run "cat $AGENT_LOG_PIN 2>/dev/null" | grep -o 'node id: [0-9a-f]\{64\}' | head -1 | awk '{print $NF}')
echo "migrating desktop EndpointId: ${NODE_ID_MIG:-<none>}"
echo "pinned desktop EndpointId:    ${NODE_ID_PIN:-<none>}"
if [ -n "${NODE_ID_MIG:-}" ] && [ "${NODE_ID_MIG:-}" = "${NODE_ID_PIN:-}" ]; then
  echo "ERROR: both agents printed the same EndpointId; the two desktop cards would show one guest twice" >&2
  exit 1
fi

echo "=== register the two DISTINCT endpoints: $HOST_A -> migrating desktop, $HOST_B -> pinned desktop ==="
reg() {
  if [ -n "$2" ]; then
    curl -s -X POST "http://$DAEDALD_LISTEN/v1/hosts/$1/vnc-endpoint" \
      -H 'Content-Type: application/json' -d "{\"node_id\":\"$2\"}" >/dev/null
  else
    echo "SKIP: no EndpointId for $1"
  fi
}
reg "$HOST_A" "${NODE_ID_MIG:-}"
reg "$HOST_B" "${NODE_ID_PIN:-}"
echo "registered: $(curl -s "http://$DAEDALD_LISTEN/v1/hosts")"

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
echo "http://localhost:$WEB_PORT/?vnc=${NODE_ID_MIG:-MISSING}"
echo "  the migrating guest has ONE tunnel node id; the desktop card follows whichever"
echo "  host owns the guest, seeded from GET /v1/migrations/guest and moved on the real"
echo "  migration_complete destination. Add &owner=host-a or &owner=host-b to override"
echo "  the seed; ?nodeA= still works as a legacy alias for ?vnc=."
echo
echo "daedald API:        http://$DAEDALD_LISTEN"
echo "current host:       $(curl -s "http://$DAEDALD_LISTEN/v1/migrations/current-host")"
echo "guest detail:       $(curl -s "http://$DAEDALD_LISTEN/v1/migrations/guest")"
echo "vnc-ws-gateway:     ${GATEWAY_ADDR:-not started}"
echo "registry:           $(curl -s "http://$DAEDALD_LISTEN/v1/hosts")"
echo
echo "$HOST_A -> desktop-a: the MIGRATING guest, $GUEST_IP:5901, ${GUEST_MEM_MIB}MiB, tunnel ${NODE_ID_MIG:-none}"
echo "$HOST_B -> desktop-b: the PINNED guest,    $PIN_IP:5901, tunnel ${NODE_ID_PIN:-none}"
echo
echo "Clicking Migrate live-migrates the desktop-a guest between $HOST_A ($TAP_A) and $HOST_B ($TAP_B)."
echo "It is never rebooted and keeps $GUEST_IP, so its VNC stream stays on the same tunnel throughout."
echo "The desktop-b guest is pinned and never migrates, so the two cards are always two different machines."
echo DESKTOP_MIGRATION_DEMO_STARTED
