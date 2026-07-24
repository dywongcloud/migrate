# daedal -- working notes for agents

Non-obvious constraints for the VM/kernel path. Read before touching it.

## Host / VMs
- macOS Apple Silicon. Two dedicated Lima VMs: `daedal-kvm` (vz, aarch64, `nestedVirtualization:true` -> real `/dev/kvm`) and `daedal-pvm` (qemu/TCG, x86_64, for the PVM kernel). The user's `hive` VM is off-limits.
- The x86_64 qemu VM requires `brew install lima-additional-guestagents`; without it `limactl start` fails with "guest agent binary could not be found for Linux-x86_64".
- Firecracker's child process opens `/dev/kvm`, so the daemon's user needs access. In production the systemd unit grants it via `SupplementaryGroups=kvm` (see `deploy/daedald.service`); the throwaway test scripts use `chmod 666 /dev/kvm`. `setfacl -m u:USER:rw /dev/kvm` is unreliable -- it grants the first firecracker but not the restore firecracker.
- Building firecracker requires `libseccomp-dev` (else the link fails with `cannot find -lseccomp`), even though a gnu-target build ships an empty runtime seccomp filter.

## PVM kernel
- The kernel source is `virt-pvm/linux` branch `pvm-612` (Linux 6.12.33). The `kernel/` tree in `dywongcloud/pvm-no-fsgsbase-rdtscp` is unusable -- it lacks all `arch/x86/**/*.S` assembly (0 `.S` files) and will not build.
- `kernel/patches/` holds five patches, all `git apply --check` clean on `pvm-612`: `0001-0004` are the no-FSGSBASE/RDTSCP series (decoded from the HTML-encoded source in the dywongcloud repo's `index.html`); `0005` is required for PVM snapshot restore (details in recall: FEAT_CTL host-initiated MSR fix).
- Host kernel config = the running Ubuntu config + `CONFIG_KVM_PVM=m`, keys cleared, BTF off, `LOCALVERSION=-pvm`, `UAPI_HEADER_TEST` off (PVM's `pvm_para.h` fails the standalone header test). The PVM kernel boots with `pti=off`.
- Guest kernel = same tree, canonical `virt-pvm/misc/pvm-guest-6.12.33.config` + `PVM_GUEST=y KVM_GUEST=y`, `make vmlinux` (uncompressed ELF firecracker boots directly). The guest rootfs must match the guest arch -- an x86_64 PVM guest needs an x86_64 busybox `/init` (an aarch64 one panics with `error -8`/ENOEXEC).
- The fallback series is inert when the CPU exposes FSGSBASE+RDTSCP (the fast path runs). To exercise it, boot with `clearcpuid=fsgsbase,rdtscp`; `dmesg` then shows "FSGSBASE not available... MSR-based" and "RDTSCP not available... trapped and emulated", and firecracker still boots a microVM via the fallback switcher.

## Firecracker migration
- The fork ships no migrate endpoint. Migration is orchestrated over `/snapshot/create` (Full+Diff), `/snapshot/load`, `/vm` pause/resume.
- Diff snapshots (`track_dirty_pages`) produce a sparse mem file; `internal/migrate/sparse.go` merges it onto the base with `SEEK_DATA`/`SEEK_HOLE`. The seek whence constants differ by OS (Linux `SEEK_DATA=3,SEEK_HOLE=4`; darwin `4,3`) -- `sparse.go` selects by `runtime.GOOS`. Real firecracker runs on Linux; the darwin path is local-dev only.
- Precopy's win is on *remote* migration (base memory streams while the guest runs; only the diff is downtime). For local migration precopy runs slightly slower than cold (two pauses, no transfer to overlap) -- expected, not a bug.
- PVM-under-qemu-TCG hits a transient firecracker exit after ~25 sustained back-to-back migrations (double-emulation artifact, no OOM/oops). The daemon's dead-instance detection marks the VM errored and rejects further migrations with 409. This does not occur on real virtualization (the KVM path is stable).

## Live migration (`api/internal/livemigrate`, `<= 30ms` blackout)
- The blackout is kept small by mapping guest memory lazily at the destination (a `File` backend mmaps the shared memfile; `Uffd` uses a userspace handler). No memory is copied during the cutover, so it is ~5ms. The one-time pre-copy Full snapshot pauses the source and scales with guest RAM (~14ms@32MiB, ~128ms@128MiB), so a small guest keeps every pause under 30ms.
- Firecracker opens the `/dev/userfaultfd` device node (not the syscall), so the Uffd backend needs `sysctl vm.unprivileged_userfaultfd=1` AND `chmod 666 /dev/userfaultfd`, alongside `chmod 666 /dev/kvm`.
- A read-only shared rootfs must be built with `mkfs.ext4 -O ^has_journal`; a journaled ext4 needs write access to replay its journal and panics read-only.
- The two "hosts" are podman `--rootfs` containers (image built by `scripts/build-host-image.sh` -- no registry pull; Lima's NAT stalls pulls) with `--network host`, a shared `/dev/shm`, and a bridge; the guest keeps its IP/MAC across hosts via Firecracker `network_overrides`. `--rootfs PATH` must come last, right before the command.

## Verification
- No unit-test files. Every check is a live `exec_js`/`curl` against a running daemon (mock backend for CI, real firecracker in the VM). `bench/ci-verify.sh` is the CI gate; `daedald bench` asserts p99 < 20s and exits non-zero otherwise. Running the workflow locally (`act` + podman) needs arm64 containers -- details in recall.
- All source is ASCII; `scripts/check-ascii.sh` is a CI step that fails on any non-ASCII byte in a tracked file (guards against homoglyphs).
