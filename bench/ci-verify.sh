#!/usr/bin/env bash
set -euo pipefail
# End-to-end verification against the mock backend: boots the daemon, exercises
# the full migration state machine, and asserts p99 < 20s. Requires no KVM/PVM,
# so it runs in CI on any Linux runner.

BIN=/tmp/daedald
STATE=$(mktemp -d)
CFG="$STATE/config.json"
cat > "$CFG" <<JSON
{
  "listen": "127.0.0.1:7071",
  "state_dir": "$STATE/state",
  "backends": { "mock": { "vcpus": 2, "mem_mib": 128 } }
}
JSON

"$BIN" serve --config "$CFG" &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true; rm -rf "$STATE"' EXIT

for i in $(seq 1 50); do
  if curl -sf http://127.0.0.1:7071/healthz >/dev/null; then break; fi
  sleep 0.1
done

echo "== capabilities =="
curl -sf http://127.0.0.1:7071/v1/capabilities

echo; echo "== error surface =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:7071/v1/vms/nope/migrate -d '{"target":"local"}')
[ "$code" = "404" ] || { echo "expected 404 for nonexistent VM, got $code"; exit 1; }

echo "== benchmark (100 precopy migrations, p99 < 20s) =="
"$BIN" bench -api http://127.0.0.1:7071 -n 100 -mode precopy -backend mock \
  -report "$STATE/bench.json" -target-ms 20000

echo "== remote peer migration =="
STATE2=$(mktemp -d)
cat > "$STATE2/config.json" <<JSON
{ "listen": "127.0.0.1:7072", "state_dir": "$STATE2/state", "backends": { "mock": { "vcpus": 2, "mem_mib": 128 } } }
JSON
"$BIN" serve --config "$STATE2/config.json" &
DAEMON2=$!
trap 'kill $DAEMON $DAEMON2 2>/dev/null || true; rm -rf "$STATE" "$STATE2"' EXIT
for i in $(seq 1 50); do curl -sf http://127.0.0.1:7072/healthz >/dev/null && break; sleep 0.1; done

VM=$(curl -sf -X POST http://127.0.0.1:7071/v1/vms -d '{"name":"ci-remote","backend":"mock","mem_mib":64}' | sed 's/.*"id":"\([^"]*\)".*/\1/')
status=$(curl -sf -X POST http://127.0.0.1:7071/v1/vms/"$VM"/migrate -d '{"target":"http://127.0.0.1:7072","mode":"precopy"}' | grep -o '"status":"[a-z]*"' | head -1)
echo "remote migration $status"
[ "$status" = '"status":"succeeded"' ] || { echo "remote migration failed"; exit 1; }

echo "ALL_CI_CHECKS_PASSED"
