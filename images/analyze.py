#!/usr/bin/env python3
"""Attribute the beacon's service gaps to the migration. Reads the per-packet
log (seq epoch_ns) and the migration report (cutover_start/end_unix_ns), then
reports:
  - cutover_gap_ms: the beacon gap that brackets the cutover window = the
    client-observed migration blackout (guest unavailable during the handoff).
  - packets, missed_seq: continuity (0 missed = no dropped service).
  - max_prep_gap_ms: the largest gap NOT at the cutover (pre-copy prep / jitter).
"""
import json
import sys

log_path = sys.argv[1]
report_path = sys.argv[2]

rep = json.load(open(report_path))
cut_start = rep["cutover_start_unix_ns"]
cut_end = rep["cutover_end_unix_ns"]

pkts = []
for line in open(log_path):
    parts = line.split()
    if len(parts) == 2:
        pkts.append((int(parts[0]), int(parts[1])))
pkts.sort(key=lambda x: x[1])

# True loss counts sequence numbers that never arrived, independent of ordering:
# UDP may reorder packets around the tap handoff without losing any.
seqs = [s for s, _ in pkts]
span = (max(seqs) - min(seqs)) if seqs else 0
missed = (span + 1) - len(set(seqs))
reordered = sum(1 for i in range(1, len(pkts)) if pkts[i][0] < pkts[i - 1][0])

# Gap analysis by arrival time: the beacon interval bounds normal spacing; a real
# service gap shows up as a gap spanning the cutover window.
cutover_gap_ms = 0.0
max_prep_gap_ms = 0.0
for i in range(1, len(pkts)):
    t0, t1 = pkts[i - 1][1], pkts[i][1]
    gap_ms = (t1 - t0) / 1e6
    if t0 <= cut_end and t1 >= cut_start:
        cutover_gap_ms = max(cutover_gap_ms, gap_ms)
    else:
        max_prep_gap_ms = max(max_prep_gap_ms, gap_ms)

result = {
    "packets": len(pkts),
    "span_seq": span,
    "missed_seq": missed,
    "reordered": reordered,
    "control_plane_blackout_ms": rep.get("blackout_ms"),
    "cutover_gap_ms": round(cutover_gap_ms, 1),
    "max_prep_gap_ms": round(max_prep_gap_ms, 1),
    "target_ms": rep.get("target_ms", 30),
}
result["pass"] = (
    result["cutover_gap_ms"] <= result["target_ms"]
    and result["missed_seq"] == 0
)
print(json.dumps(result, indent=2))
