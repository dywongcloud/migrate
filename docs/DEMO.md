# Demo (record a <= 120s video)

This is the exact sequence to record. It runs entirely inside the `daedal-kvm`
Lima VM, which provides nested KVM on macOS. One command produces the whole
result; the rest of the video is narration.

## One-time setup (not recorded)

```sh
limactl start --name=daedal-kvm lima/daedal-kvm.yaml
# inside the VM: build firecracker + the uffd handler, fetch the guest kernel,
# build the rootfs and the container host image (see README "Live migration").
```

## The recording

Open a terminal into the VM and run the two-host demo:

```sh
limactl shell daedal-kvm -- bash scripts/two-host-demo.sh
```

### Narration (about 90 seconds)

1. **(0:00-0:15) The setup.** "Two containers, `host-a` and `host-b`, are two
   independent Firecracker hosts. `host-a` is running a microVM whose only job is
   to send a UDP heartbeat -- a sequence number every millisecond -- to a
   collector. That beacon is the 'user experience' we must not interrupt."

2. **(0:15-0:35) The mechanism.** "To migrate with almost no blackout, the guest's
   memory is pre-copied to a tmpfs both hosts share, then kept current with diff
   snapshots while the guest keeps running. Only a tiny final delta is captured at
   the cutover, and the destination brings the guest back with its memory mapped
   lazily -- so no memory is copied during the blackout itself."

3. **(0:35-0:60) Run it.** Point at the output:
   - `blackout=...ms` -- the control-plane cutover: the guest stops on `host-a`
     and is running on `host-b` in single-digit milliseconds.
   - `cutover_gap_ms` and `missed_seq=0` from the collector -- the client saw no
     gap at the handoff and **lost zero packets**. The guest kept its IP and MAC
     across hosts (Firecracker `network_overrides` re-homes the NIC on `host-b`'s
     tap), so the connection never noticed the move.

4. **(0:60-0:80) The numbers.** "Across 20 migrations the p99 guest blackout is
   about 10 ms -- comfortably inside the 30 ms budget. Every guest pause in the
   whole migration, including the one-time full-memory pre-copy, stays under 30 ms
   for this guest size."

5. **(0:80-0:90) Close.** "Firecracker snapshots, userfaultfd, a shared memory
   image, and a NIC re-home -- that's a real live migration under 30 ms of
   blackout with an uninterrupted workload."

## What to have on screen

- The `scripts/two-host-demo.sh` output (blackout + analyze JSON).
- Optionally `podman ps` showing `host-a` and `host-b` running concurrently.
- Optionally `bench/RESULTS-livemigrate.md` for the p99 table.
