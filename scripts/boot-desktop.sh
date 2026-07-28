#!/bin/bash
set -euxo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
B=/Users/dylanwong/daedal/kernel/build
FC=/usr/local/bin/firecracker
BR=${BR:-br0}
TAP=${TAP:-tap-desk}
HOST_IP=${HOST_IP:-172.20.0.1}
GUEST_MAC=${GUEST_MAC:-06:00:AC:14:00:03}
RUN=${RUN:-/tmp/fc-desktop}
API_SOCK=$RUN/fc.sock
LOG=$RUN/serial.log

sudo install -m0755 "$B/firecracker-aarch64" "$FC"
sudo chmod 666 /dev/kvm
pkill -f "firecracker --api-sock $API_SOCK" 2>/dev/null || true
sleep 0.3
rm -rf "$RUN"
mkdir -p "$RUN"
mkfifo "$RUN/console.in"

if ! ip link show "$BR" >/dev/null 2>&1; then
  sudo ip link add "$BR" type bridge
  sudo ip addr add "$HOST_IP/24" dev "$BR"
  sudo ip link set "$BR" up
fi
UPLINK=$(ip route show default | awk '/default/ {print $5; exit}')
if [ -n "$UPLINK" ] && ! sudo iptables -t nat -C POSTROUTING -s "${HOST_IP%.*}.0/24" -o "$UPLINK" -j MASQUERADE 2>/dev/null; then
  sudo iptables -t nat -A POSTROUTING -s "${HOST_IP%.*}.0/24" -o "$UPLINK" -j MASQUERADE
fi
sudo ip link del "$TAP" 2>/dev/null || true
sudo ip tuntap add "$TAP" mode tap
sudo ip link set "$TAP" master "$BR"
sudo ip link set "$TAP" up

api(){ curl -s --unix-socket "$API_SOCK" -X "$1" "http://localhost$2" -H 'Content-Type: application/json' -d "$3"; }

nohup "$FC" --api-sock "$API_SOCK" 0<> "$RUN/console.in" > "$LOG" 2>&1 &
echo $! > "$RUN/fc.pid"
disown
for i in $(seq 1 100); do [ -S "$API_SOCK" ] && break; sleep 0.05; done

api PUT /boot-source '{"kernel_image_path":"'$B'/vmlinux-aarch64","boot_args":"console=ttyS0 reboot=k panic=1 pci=off net.ifnames=0 biosdevname=0 root=/dev/vda rw init=/sbin/init"}'
api PUT /drives/rootfs '{"drive_id":"rootfs","path_on_host":"'$B'/rootfs-desktop.ext4","is_root_device":true,"is_read_only":false}'
api PUT /machine-config '{"vcpu_count":2,"mem_size_mib":1024}'
api PUT /network-interfaces/eth0 '{"iface_id":"eth0","guest_mac":"'$GUEST_MAC'","host_dev_name":"'$TAP'"}'
api PUT /actions '{"action_type":"InstanceStart"}'
echo
echo "api_sock=$API_SOCK serial_log=$LOG fc_pid=$(cat "$RUN/fc.pid")"
echo BOOT_DESKTOP_STARTED
