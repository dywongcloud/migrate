# daedal — sub-20s Firecracker microVM migration on KVM and PVM

`daedal` is a migration API for [Firecracker](https://github.com/firecracker-microvm/firecracker)
microVMs that live-migrates a running guest — memory, device state, vCPU state —
from one VMM process to another (locally or across hosts) with a **p99 total
migration time under 20 seconds**.

It runs on two backends from a single binary:

- **KVM** — ordinary hardware-assisted virtualization (`/dev/kvm`).
- **PVM** — [Pagetable-based Virtual Machine](https://github.com/virt-pvm/linux),
  a software hypervisor that provides `/dev/kvm` **without** VT-x/AMD-V or nested
  virtualization, using the [DecOperations/firecracker-next](https://github.com/DecOperations/firecracker-next)
  fork plus the [`pvm-no-fsgsbase-rdtscp`](https://github.com/dywongcloud/pvm-no-fsgsbase-rdtscp)
  kernel series so it works even on cloud CPUs that mask `FSGSBASE`/`RDTSCP`.

The starting point is `firecracker-next`; migration is orchestrated on top of its
snapshot API (`/snapshot/create` Full+Diff, `/snapshot/load`, `/vm` pause/resume)
because Firecracker ships no migrate endpoint of its own.

## What migration does

Firecracker has no live "migrate" call. `daedald` builds one out of the snapshot
primitives:

```
precopy (default, for remote):
  pause → Full snapshot → resume        (base memory captured; guest keeps running)
  stream base memory to dest            (NOT counted as downtime — guest is live)
  pause → Diff snapshot                 (only pages dirtied since base)
  stream diff + merge onto base at dest
  restore + resume at dest              (downtime = final pause → resume)

cold (for local / tiny guests):
  pause → Full snapshot → restore + resume
```

`track_dirty_pages` (Firecracker's dirty-bitmap) is what makes the diff small, so
the final pause is short. **Total** time is dominated by the base transfer (guest
running); **downtime** is only the final diff+restore. The API reports both.

## Architecture

```
                 daedald (Go, one static binary, GOARCH amd64|arm64)
   ┌─────────────────────────────────────────────────────────────────┐
   │ HTTP API  ── vms CRUD ── /migrate ── /metrics(p99) ── /peer/*     │
   │                                                                   │
   │ store        migrate.Manager           metrics.Recorder           │
   │ (VM state    (orchestration,           (per-migration durations,  │
   │  machine)     rollback, peer proto)     p50/p95/p99, histogram)   │
   │                    │                                              │
   │              vmm.Backend (interface)                              │
   │              ├── FirecrackerBackend  (spawn fc, unix-socket API)  │
   │              └── MockBackend         (CI, no KVM/PVM needed)      │
   └───────────────────────────┬───────────────────────────────────────┘
                                │  same binary, backend chosen by
                                │  DetectCapabilities(/dev/kvm, /proc/modules)
             ┌──────────────────┴──────────────────┐
        KVM backend                           PVM backend
     /dev/kvm (hardware)                  /dev/kvm (kvm-pvm.ko, software)
     firecracker-aarch64                  firecracker-x86_64
     guest: FC CI vmlinux                 guest: vmlinux-pvmguest
```

The backend is **not** a fork in the code — `DetectCapabilities` probes `/dev/kvm`
and `/proc/modules` for `kvm_pvm`, picks a default, and the same orchestration path
drives both. KVM vs PVM differ only in which `firecracker` binary and guest kernel
a backend config points at.

## Repository layout

| Path | What |
|------|------|
| `api/` | The `daedald` daemon (Go). `cmd/daedald` = serve + bench; `internal/{vmm,store,migrate,metrics,server}`. |
| `lima/` | Lima VM templates: `daedal-kvm.yaml` (vz, nested virt), `daedal-pvm.yaml` (qemu/TCG x86_64). |
| `kernel/patches/` | The 4-patch `no-FSGSBASE/RDTSCP` RFC series, decoded and apply-clean on `virt-pvm/linux` `pvm-612`. |
| `scripts/` | Kernel/firecracker/rootfs build scripts, run inside the Lima VMs. |
| `deploy/` | `systemd` unit + example backend configs. |
| `bench/` | `ci-verify.sh` (mock e2e + p99 assertion) and benchmark reports. |
| `.github/workflows/ci.yml` | Build both arches + mock-backend e2e on every push. |

## From-scratch setup on macOS (Apple Silicon)

Everything runs inside Lima VMs because Firecracker needs a Linux host. The user's
own Lima VMs are never touched; `daedal` uses dedicated `daedal-kvm` / `daedal-pvm`
instances.

### 0. Host prerequisites

```sh
brew install lima qemu lima-additional-guestagents go
```

`lima-additional-guestagents` is required for the x86_64 (qemu/TCG) VM — without it
`limactl start` on an x86_64 template fails with "guest agent binary could not be
found for Linux-x86_64".

### 1. Build the daemon

```sh
cd api
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ../bin/daedald-linux-arm64 ./cmd/daedald
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../bin/daedald-linux-amd64 ./cmd/daedald
```

### 2. KVM backend (native, fast)

```sh
limactl start --name=daedal-kvm lima/daedal-kvm.yaml     # vz + nestedVirtualization:true
limactl shell daedal-kvm -- ls -l /dev/kvm               # must exist

# in the VM:
bash scripts/build-firecracker.sh aarch64-unknown-linux-gnu firecracker-aarch64 4
curl -fsSL -o kernel/build/vmlinux-aarch64 \
  https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.12/aarch64/vmlinux-6.1.128
bash scripts/build-rootfs.sh
```

Then deploy `bin/daedald-linux-arm64`, `deploy/config.kvm.json`, and
`deploy/daedald.service` into the VM (see step 4).

### 3. PVM backend (no hardware virt)

```sh
limactl start --name=daedal-pvm lima/daedal-pvm.yaml

# build the patched host kernel (cross-compiled x86_64) — reuses the ansible recipe
# from firecracker-next/pvm-firecracker-ansible, config derived from the running
# Ubuntu config + CONFIG_KVM_PVM=m + the 4 no-FSGSBASE/RDTSCP patches:
bash scripts/build-host-kernel.sh        # -> kernel/build/{bzImage-pvm, kvm-pvm.ko}
bash scripts/build-guest-kernel.sh       # -> kernel/build/vmlinux-pvmguest

# install into daedal-pvm, reboot into 6.12.33-pvm, load the module:
#   grub default = new kernel, boot arg pti=off, modprobe kvm-pvm
# (see scripts/build-host-kernel.sh + firecracker-next/pvm-firecracker-ansible/tasks/60-boot.yml)
```

To exercise the fallback paths (the point of the `pvm-no-fsgsbase-rdtscp` series),
boot `daedal-pvm` with a CPU model that masks the features, then look for
`FSGSBASE not available; the switcher will use the slower MSR-based ...` and
`RDTSCP not available; ... trapped and emulated` in `dmesg`.

### 4. Run the daemon

```sh
sudo install -m0755 bin/daedald-linux-<arch> /usr/local/bin/daedald
sudo mkdir -p /etc/daedald && sudo cp deploy/config.<kvm|pvm>.json /etc/daedald/config.json
sudo cp deploy/daedald.service /etc/systemd/system/
sudo systemctl enable --now daedald
curl -s localhost:7031/v1/capabilities
```

## Using the API

```sh
# create + boot a microVM (backend auto-detected)
curl -s localhost:7031/v1/vms -d '{"name":"web","backend":"auto","mem_mib":256}'

# migrate it to a peer daemon on another host
curl -s localhost:7031/v1/vms/<id>/migrate \
  -d '{"target":"http://10.0.0.2:7031","mode":"precopy","transfer_disk":true}'

# migration timing + p99 across everything so far
curl -s localhost:7031/v1/metrics | jq .total_ms
```

Full request/response shapes: `GET /spec` (served OpenAPI 3), source
`api/internal/server/openapi.yaml`.

## Benchmarking the 20s SLA

```sh
daedald bench -api http://localhost:7031 -n 200 -mode precopy -backend auto \
  -mem 512 -report bench/results/pvm-512.json -target-ms 20000
```

Exits non-zero if p99 ≥ 20s. `-backend mock` needs no VM and runs anywhere (this is
the CI gate in `.github/workflows/ci.yml`).
