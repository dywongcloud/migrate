#!/bin/bash
set -uo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
API=http://127.0.0.1:7031

sudo pkill -f daedald 2>/dev/null || true
sleep 1
sudo chmod 666 /dev/kvm
sudo install -m0755 /Users/dylanwong/daedal/bin/daedald-linux-arm64 /usr/local/bin/daedald
sudo mkdir -p /etc/daedald /var/lib/daedald/images
sudo cp "$B/vmlinux-aarch64" /var/lib/daedald/images/vmlinux-kvm
sudo cp "$B/rootfs.ext4" /var/lib/daedald/images/rootfs.ext4
sudo install -m0755 "$B/firecracker-aarch64" /usr/local/bin/firecracker
sudo cp /Users/dylanwong/daedal/deploy/config.kvm.json /etc/daedald/config.json
sudo cp /Users/dylanwong/daedal/deploy/daedald.service /etc/systemd/system/

sudo systemctl daemon-reload
sudo systemctl enable daedald >/dev/null 2>&1
sudo systemctl restart daedald
sleep 2
echo "active_state=$(systemctl is-active daedald)"
echo "health=$(curl -s $API/healthz)"
echo "caps=$(curl -s $API/v1/capabilities)"

MAINPID=$(systemctl show -p MainPID --value daedald)
echo "-- kill main pid $MAINPID, expect systemd Restart=on-failure --"
sudo kill -9 "$MAINPID"
sleep 4
echo "active_after_kill=$(systemctl is-active daedald)"
NEWPID=$(systemctl show -p MainPID --value daedald)
echo "restarted: old=$MAINPID new=$NEWPID (different => auto-restarted)"
echo "health_after_restart=$(curl -s $API/healthz)"

sudo systemctl stop daedald
echo "stopped_state=$(systemctl is-active daedald)"
echo SYSTEMD_TEST_DONE
