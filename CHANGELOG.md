# Changelog

## eec2f38 — initial release

daedal: a Go migration daemon (`daedald`) that live-migrates real Firecracker
microVMs with p99 total migration time under 20s, on KVM (hardware `/dev/kvm`)
and PVM (software `/dev/kvm` via kvm-pvm, no hardware virtualization) from one
static binary.

- Migration orchestrated over Firecracker's snapshot API (pause → Full/Diff
  snapshot → transfer → load → resume); local and remote sha256-streamed peer
  protocol; rollback, concurrent-migration serialization, dead-instance detection.
- Metrics with p50/p95/p99 + histogram; OpenAPI served at `/spec`; mock backend
  for CI (`bench/ci-verify.sh`).
- Witnessed real Firecracker migration: KVM p99 688ms–4.0s (128MiB–1GiB),
  PVM/TCG p99 1.4–2.1s, all under the 20s SLA; guest memory continuity proven.
- PVM host+guest kernels cross-compiled from `virt-pvm/linux pvm-612` with the
  no-FSGSBASE/RDTSCP series (`kernel/patches/0001-0004`) plus a fix allowing
  host-initiated `MSR_IA32_FEAT_CTL` (`0005`) required for PVM snapshot restore.
- Lima templates, build scripts, systemd unit, and a from-scratch macOS runbook.
