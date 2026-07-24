# shellcheck shell=bash
# Sourced by scripts that must run INSIDE the Linux Lima VM (daedal-kvm for the
# KVM path, daedal-pvm for the PVM path), not on the macOS host. Those scripts use
# Linux-only tooling (mkfs.ext4, mount -o loop, ip, podman, make, /dev/kvm) that
# does not exist on macOS, so running them on the host aborts with a cryptic
# "command not found". require_linux turns that into an actionable, fail-fast error.
#
# Usage, right after the shebang and `set -...` line:
#   . "$(dirname "$0")/lib/guard.sh"
#   require_linux              # OS check only
#   require_linux mkfs.ext4    # OS check plus specific command(s)

require_linux() {
  if [ "$(uname -s)" != "Linux" ]; then
    script=$(basename "$0")
    {
      echo "ERROR: $script uses Linux-only tooling and cannot run on the macOS host."
      echo "Run it INSIDE the Lima VM instead (the repo is mounted at the same path):"
      echo
      echo "    limactl shell daedal-kvm      # or daedal-pvm for the PVM path"
      echo "    cd /Users/dylanwong/daedal"
      echo "    bash scripts/$script"
      echo
      echo "See AGENTS.md \"Host / VMs\" for the VM setup."
    } >&2
    exit 1
  fi
  missing=
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
  done
  if [ -n "$missing" ]; then
    script=$(basename "$0")
    {
      echo "ERROR: $script needs command(s) not found on this host:$missing"
      echo "Install them inside the Lima VM (e.g. 'sudo apt-get install e2fsprogs'"
      echo "for mkfs.ext4), then re-run: bash scripts/$script"
    } >&2
    exit 1
  fi
}
