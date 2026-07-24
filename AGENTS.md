# daedal — working notes for agents

Non-obvious constraints discovered building this. Read before touching the VM/kernel path.

## Host / VMs
- macOS Apple Silicon. Two dedicated Lima VMs: `daedal-kvm` (vz, aarch64, `nestedVirtualization:true` → real `/dev/kvm`) and `daedal-pvm` (qemu/TCG, x86_64, for the PVM kernel). Never touch the user's `hive` VM.
- The x86_64 qemu VM needs `brew install lima-additional-guestagents` or `limactl start` fails with "guest agent binary could not be found for Linux-x86_64".
- Firecracker's child process opens `/dev/kvm`. In the throwaway test VM we `chmod 666 /dev/kvm`; in production use `SupplementaryGroups=kvm` on the systemd unit (see `deploy/daedald.service`). A `setfacl -m u:USER:rw /dev/kvm` was observed to let the *first* firecracker boot but not the *restore* firecracker — do not rely on it.
- Building firecracker needs `libseccomp-dev` (link error `cannot find -lseccomp` otherwise), even though a gnu-target build uses an empty runtime seccomp filter.

## PVM kernel
- The real kernel source is `virt-pvm/linux` branch `pvm-612` (Linux 6.12.33), NOT the `kernel/` tree in `dywongcloud/pvm-no-fsgsbase-rdtscp` — that upload dropped all `arch/x86/**/*.S` assembly (0 `.S` files) and won't build.
- The useful artifact from `dywongcloud/pvm-no-fsgsbase-rdtscp` is the 4-patch `no-FSGSBASE/RDTSCP` RFC series, embedded HTML-encoded in `index.html`. It is decoded into `kernel/patches/000[1-4]-*.patch` here; all four `git apply --check` clean on `pvm-612`. Patches 1-3 are host-side (switcher + KVM emulator + pvm vendor), patch 4 is guest-side (`arch/x86/kernel/pvm.c`).
- Host kernel config = the running Ubuntu config + `CONFIG_KVM_PVM=m`, keys cleared, BTF off, `LOCALVERSION=-pvm`, `UAPI_HEADER_TEST` off (PVM's `pvm_para.h` fails the standalone header test). PVM needs `pti=off` at boot.
- Guest kernel = same tree, canonical `virt-pvm/misc/pvm-guest-6.12.33.config` + `PVM_GUEST=y KVM_GUEST=y`, `make vmlinux` (uncompressed ELF firecracker boots directly).
- To *exercise* the fallback series (the whole point), boot with a CPU that masks `fsgsbase`+`rdtscp` and look for the `pr_info` lines "FSGSBASE not available… MSR-based" / "RDTSCP not available… trapped and emulated" in `dmesg`.

## Firecracker migration
- The fork ships no migrate endpoint. Migration is orchestrated over `/snapshot/create` (Full+Diff), `/snapshot/load`, `/vm` pause/resume.
- Diff snapshots (`track_dirty_pages`) produce a sparse mem file; `internal/migrate/sparse.go` merges it onto the base with `SEEK_DATA`/`SEEK_HOLE`. **The seek whence constants differ by OS** (Linux `SEEK_DATA=3,SEEK_HOLE=4`; darwin `4,3`) — `sparse.go` picks by `runtime.GOOS`. Real firecracker runs on Linux; the darwin path only matters for local dev.
- Precopy vs cold: precopy's win is on *remote* migration (base memory streams while the guest runs, only the diff is downtime). For local migration precopy is slightly slower (two pauses, no transfer to overlap) — expected, not a bug.

## Verification
- No unit-test files. Every check is a live `exec_js`/`curl` against a running daemon (mock backend for CI, real firecracker in the VM). `bench/ci-verify.sh` is the CI gate; `daedald bench` asserts p99 < 20s and exits non-zero otherwise.
