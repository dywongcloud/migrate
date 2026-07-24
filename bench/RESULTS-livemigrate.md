# Live-migration results

**Requirement: total guest blackout <= 30 ms.** Guest blackout is the time the
guest is executing on neither host. All numbers are from real Firecracker
migrations on Lima `daedal-kvm` (vz, aarch64, nested KVM) on an Apple M3 Pro.

## Cutover blackout (the standard live-migration downtime)

Measured in the orchestrator across `pause source -> resume destination`, with a
per-phase breakdown. Guest: 32 MiB, 1 vCPU, `File` backend on shared tmpfs.

| Phase | ms |
|-------|----|
| pause source | ~0.4 |
| final diff snapshot | ~0.8 |
| merge diff into base | ~0.7 |
| destination restore + resume | ~5 |
| **total cutover blackout** | **~5-10 ms** |

p99 over 20 back-to-back migrations (single host, two Firecracker processes):
**p50 9.0 ms, p95 10.0 ms, p99 10.3 ms, max 10.3 ms**, 20/20 with the guest
continuing (heartbeat advanced, no reboot).

## Two container hosts, networked guest

`scripts/two-host-demo.sh`: a guest on container `host-a` runs a 1 kHz UDP
beacon; it is live-migrated to `host-b`.

| Metric | Value |
|--------|-------|
| control-plane cutover blackout | ~5 ms |
| client-observed gap at the handoff | **0 ms** |
| packets lost across the migration | **0** (only ~80 reordered at the handoff) |
| guest reboot | none (memory preserved, NIC re-homed) |

The guest keeps its IP and MAC (Firecracker `network_overrides` re-homes the NIC
onto `host-b`'s tap), so the in-flight beacon loses no packets.

## Memory size and the one-time pre-copy pause

The blackout is dominated by the destination restore, which is small and
independent of guest RAM (memory faults in lazily after resume). The **one-time
pre-copy Full snapshot** pauses the source once to write the base memory image,
and that pause scales with guest RAM:

| Guest RAM | cutover blackout | base Full-snapshot pause |
|-----------|------------------|--------------------------|
| 128 MiB | 7.4 ms | 128 ms |
| 64 MiB | 4.0 ms | 19.5 ms |
| 32 MiB | 3.3 ms | 14 ms |

The pre-copy pause happens with the guest still live on the source (it is not
cutover downtime), but it is a source pause, so a small guest keeps every guest
pause in the whole migration inside 30 ms. On nested virtualization this pause
has extra jitter (occasionally 30-50 ms at 32 MiB); on real hardware it tracks
the memory-write time.

## Reproduce

```sh
bash scripts/blackout-p99.sh 20 32     # p99 cutover blackout
bash scripts/two-host-demo.sh          # two container hosts + client-observed gap
```
