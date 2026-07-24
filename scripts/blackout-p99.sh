#!/bin/bash
# Run N live migrations and report p50/p99 guest blackout, asserting p99 <= 30ms.
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
N=${1:-20}
MEM=${2:-128}
BIN=/Users/dylanwong/daedal/bin/daedald-linux-arm64
UFFD=$(find ~/fc/firecracker-next/build -name uffd_on_demand_handler -type f -executable | head -1)
sudo chmod 666 /dev/kvm; sudo sysctl -w vm.unprivileged_userfaultfd=1 >/dev/null; sudo chmod 666 /dev/userfaultfd
sudo install -m0755 /Users/dylanwong/daedal/kernel/build/firecracker-aarch64 /usr/local/bin/firecracker
OUT=/tmp/p99; rm -rf "$OUT"; mkdir -p "$OUT"

for i in $(seq 1 "$N"); do
  sudo pkill -f 'firecracker --api-sock' 2>/dev/null; sudo pkill -f uffd_on_demand 2>/dev/null
  "$BIN" livemigrate -firecracker /usr/local/bin/firecracker -uffd-handler "$UFFD" \
    -kernel /Users/dylanwong/daedal/kernel/build/vmlinux-aarch64 \
    -rootfs /Users/dylanwong/daedal/kernel/build/rootfs.ext4 \
    -mem "$MEM" -precopy-rounds 3 -target-ms 30 \
    -shared-dir /dev/shm/p99-$i -work-dir /tmp/p99/w-$i \
    -report "$OUT/r-$i.json" > "$OUT/log-$i" 2>&1
  B=$(grep -oE 'blackout=[0-9.]+' "$OUT/log-$i" | head -1 | cut -d= -f2)
  echo "run $i: blackout=${B}ms $(grep -o 'continued=[a-z]*' "$OUT/log-$i" | head -1)"
  rm -rf /dev/shm/p99-$i
done

echo "=== percentiles over $N runs ==="
python3 - "$OUT" <<'PY'
import json, glob, sys, statistics
d=sys.argv[1]
bs=[]; cont=0; n=0
for f in sorted(glob.glob(d+'/r-*.json')):
    j=json.load(open(f)); bs.append(j['blackout_ms']); n+=1
    if j.get('continued'): cont+=1
bs.sort()
def pct(p):
    i=max(0,int(p*len(bs)+0.5)-1); return bs[min(i,len(bs)-1)]
print(f"n={n} continued={cont}/{n} min={bs[0]:.1f} p50={pct(.5):.1f} p95={pct(.95):.1f} p99={pct(.99):.1f} max={bs[-1]:.1f} ms")
print("PASS" if bs and pct(.99)<=30 and cont==n else "FAIL")
PY
echo P99_DONE
