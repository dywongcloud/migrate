# Changelog

## 5a7e4ae -- live migration with <=30ms blackout

Live-migrate a running Firecracker microVM between two hosts with a guest
blackout of single-digit milliseconds, without dropping an in-guest workload.

- `api/internal/livemigrate`: pre-copy (Full snapshot to shared tmpfs, refreshed
  by Diff snapshots while the guest runs) + single-process cutover (tiny final
  diff, destination restore with memory mapped lazily via `File` or `Uffd`
  backend, guest NIC re-homed with `network_overrides`). Rollback on failure.
- CLI `daedald livemigrate` + REST API `daedald livemigrate serve`
  (POST /v1/migrations, GET /v1/metrics with blackout p50/p95/p99).
- `images/`: in-guest UDP beacon + collector/analyzer measuring the
  client-observed blackout and proving zero packet loss across the handoff.
- `scripts/`: build scripts, single-host p99 harness, two-container-host demo.
- Measured: cutover blackout ~5ms (p99 ~10ms/20 runs), 0 packets lost, two
  concurrent podman host containers. All source ASCII (CI enforces it).

## eec2f38 -- initial release

daedal: a Go migration daemon (`daedald`) that live-migrates real Firecracker
microVMs with p99 total migration time under 20s, on KVM (hardware `/dev/kvm`)
and PVM (software `/dev/kvm` via kvm-pvm, no hardware virtualization) from one
static binary.

- Migration orchestrated over Firecracker's snapshot API (pause -> Full/Diff
  snapshot -> transfer -> load -> resume); local and remote sha256-streamed peer
  protocol; rollback, concurrent-migration serialization, dead-instance detection.
- Metrics with p50/p95/p99 + histogram; OpenAPI served at `/spec`; mock backend
  for CI (`bench/ci-verify.sh`).
- Witnessed real Firecracker migration: KVM p99 688ms-4.0s (128MiB-1GiB),
  PVM/TCG p99 1.4-2.1s, all under the 20s SLA; guest memory continuity proven.
- PVM host+guest kernels cross-compiled from `virt-pvm/linux pvm-612` with the
  no-FSGSBASE/RDTSCP series (`kernel/patches/0001-0004`) plus a fix allowing
  host-initiated `MSR_IA32_FEAT_CTL` (`0005`) required for PVM snapshot restore.
- Lima templates, build scripts, systemd unit, and a from-scratch macOS runbook.
