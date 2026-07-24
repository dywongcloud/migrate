#!/usr/bin/env python3
"""Collector: receive the guest's UDP sequence beacon and log each packet's
sequence number and wall-clock arrival time. analyze.py correlates the log with
the migration's cutover window to report the client-observed blackout."""
import socket
import sys
import time

host = sys.argv[1] if len(sys.argv) > 1 else "0.0.0.0"
port = int(sys.argv[2]) if len(sys.argv) > 2 else 9999
duration = float(sys.argv[3]) if len(sys.argv) > 3 else 25.0
out_path = sys.argv[4] if len(sys.argv) > 4 else "/tmp/beacon-log.txt"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 16 * 1024 * 1024)
s.bind((host, port))
s.settimeout(1.0)

start = time.monotonic()
with open(out_path, "w") as f:
    while time.monotonic() - start < duration:
        try:
            data, _ = s.recvfrom(64)
        except socket.timeout:
            continue
        now_ns = time.time_ns()
        try:
            seq = int(data)
        except ValueError:
            continue
        f.write(f"{seq} {now_ns}\n")
print("collector wrote", out_path)
