# daedal -- Firecracker live VM migration with <= 30 ms blackout

`daedal` live-migrates a running [Firecracker](https://github.com/firecracker-microvm/firecracker)
microVM from one host to another with a **guest blackout (the time the guest is
executing on neither host) in the single-digit milliseconds**, while a workload
running inside the guest keeps serving without a dropped connection.

Measured, end to end, between two container hosts:

| Metric | Value |
|--------|-------|
| Guest blackout at cutover (control plane) | **~5 ms** (p99 ~10 ms over 20 runs) |
| Client-observed gap at the handoff | **0 ms** |
| Packets lost across the migration | **0** |
| Blackout budget | 30 ms |

The guest keeps its IP and MAC across the move, so an in-flight UDP beacon it is
sending loses zero packets -- the connection never notices the host switch.

## How the blackout stays small

Firecracker has no live-migration call, and its snapshot pauses the VM to
serialize memory -- so a naive "snapshot, copy, restore" pays the whole
memory-copy cost as downtime. The trick is to keep memory *out of the blackout
window*:

```
pre-copy (guest live on the source):
  Full snapshot -> base memory image on a tmpfs both hosts share
  refresh it with Diff snapshots while the guest keeps running

cutover (the blackout -- guest paused on neither-yet-both hosts):
  pause source -> tiny final Diff (only pages dirtied since the last refresh)
  destination restores vCPU/device state and resumes
  guest memory is mmap'd from the shared image and faults in lazily AFTER resume
  the guest NIC is re-homed onto the destination host's tap (network_overrides)
```

Because the memory image is already on shared storage and the destination maps it
lazily (a `File` backend via the kernel page cache, or `Uffd` for a true
post-copy over a network), **no memory is copied during the blackout**. The
blackout is just: final tiny diff + restore vCPU/device state + resume, measured
at ~5 ms. Every guest pause in the whole migration -- including the one-time
full-memory pre-copy snapshot -- stays inside 30 ms for a small guest.

Details and the primitives used are in `docs/livemigrate.md`.

## Layout

| Path | What |
|------|------|
| `api/internal/livemigrate/` | the migration orchestrator (pre-copy, cutover, blackout timing) |
| `api/cmd/daedald/livemigrate*.go` | CLI + REST API (`daedald livemigrate`, `daedald livemigrate serve`) |
| `images/` | the in-guest UDP beacon (`beacon.c`) and the host-side collector/analyzer |
| `scripts/` | build scripts, the single-host and two-container demos, the p99 harness |
| `bench/` | measured results (`RESULTS-livemigrate.md`) |
| `docs/DEMO.md` | the <=120s video walkthrough |
| `AI-USAGE.md` | how AI was used (disclosed, per the challenge policy) |

The repo also contains an earlier snapshot-based migration service (`daedald
serve`, `internal/migrate`) and a PVM/no-hardware-virt kernel path; those are the
foundation this live-migration work builds on.

## Run it

Everything runs inside a Lima VM that provides nested KVM on macOS (`daedal-kvm`).
The `scripts/` use Linux-only tooling (`mkfs.ext4`, `ip`, `podman`, `/dev/kvm`),
so run them from a shell *inside* the VM, not on the macOS host -- the repo is
mounted at the same path inside the VM. Run on the host by mistake, each script
fails fast with a pointer here instead of a cryptic `command not found`.

```sh
# open a shell inside the VM first (the repo is mounted at the same path)
limactl shell daedal-kvm
cd /Users/dylanwong/daedal

# build firecracker + the uffd handler, fetch the guest kernel,
# build the rootfs and the self-contained container "host" image
bash scripts/build-rootfs.sh
bash scripts/build-net-rootfs.sh          # networked guest with the UDP beacon
bash scripts/build-host-image.sh          # container rootfs (no registry pull)

# single-host mechanism + p99 (two Firecracker processes)
bash scripts/blackout-p99.sh 100 32

# the headline: two container hosts, live migration, client-observed blackout
bash scripts/two-host-demo.sh
```

`sudo sysctl -w vm.unprivileged_userfaultfd=1` and world-access to `/dev/kvm` and
`/dev/userfaultfd` are needed for the (unprivileged) Firecracker to use KVM and
userfaultfd; the demo scripts set these.

## The migration API

```sh
daedald livemigrate serve -listen :7040 -firecracker ... -uffd-handler ... \
  -kernel ... -rootfs ...

curl -s localhost:7040/v1/migrations -d '{"mem_mib":32}'   # run one, get its blackout
curl -s localhost:7040/v1/metrics                          # blackout p50/p95/p99
```

The same orchestration is a Go package (`livemigrate.Run(Config)`) and a one-shot
CLI (`daedald livemigrate ...`, used by the demos and the p99 harness).
