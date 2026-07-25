# shellcheck shell=bash
# Sourced by scripts that must run INSIDE the Linux Lima VM (daedal-kvm for the
# KVM path, daedal-pvm for the PVM path): they use Linux-only tooling
# (mkfs.ext4, mount -o loop, ip, podman, make, /dev/kvm) that does not exist on
# macOS. Lima mounts the repo at the same path inside the VM, so when a guarded
# script is run on the macOS host, require_linux transparently re-executes it
# inside the VM (arguments and exit code preserved) instead of failing with a
# cryptic "command not found".
#
# Knobs: DAEDAL_VM overrides the target VM (default daedal-kvm, e.g. daedal-pvm
# for the PVM path); DAEDAL_NO_FORWARD=1 disables forwarding and fails fast with
# the actionable error instead.
#
# Usage, right after the shebang and `set -...` line:
#   . "$(dirname "$0")/lib/guard.sh"
#   require_linux              # OS check only
#   require_linux mkfs.ext4    # OS check plus specific command(s)

# Captured at source time: the calling script's own arguments, quoted so they
# survive the trip through `bash -lc` (bash-3.2-safe; no arrays under set -u).
GUARD_FWD_ARGS=""
for guard_fwd_a in "$@"; do
  GUARD_FWD_ARGS="$GUARD_FWD_ARGS $(printf %q "$guard_fwd_a")"
done

require_linux() {
  if [ "$(uname -s)" != "Linux" ]; then
    script=$(basename "$0")
    repo=$(cd "$(dirname "$0")/.." && pwd)
    vm="${DAEDAL_VM:-daedal-kvm}"
    if [ -z "${DAEDAL_NO_FORWARD:-}" ] && command -v limactl >/dev/null 2>&1 \
       && limactl list 2>/dev/null | awk -v v="$vm" '$1 == v && $2 == "Running" { found = 1 } END { exit !found }'; then
      echo "guard: $script needs Linux; running it inside the '$vm' VM (DAEDAL_NO_FORWARD=1 disables)" >&2
      exec limactl shell "$vm" -- bash -lc \
        "cd $(printf %q "$repo") && DAEDAL_NO_FORWARD=1 bash $(printf %q "scripts/$script")$GUARD_FWD_ARGS"
    fi
    {
      echo "ERROR: $script uses Linux-only tooling and cannot run on the macOS host,"
      echo "and auto-forwarding is unavailable (limactl missing, VM '$vm' not Running,"
      echo "or DAEDAL_NO_FORWARD set)."
      echo
      echo "Start the VM, then re-run -- the script forwards into it automatically:"
      echo
      echo "    limactl start $vm"
      echo "    bash scripts/$script"
      echo
      echo "Or run it manually inside the VM (the repo is mounted at the same path):"
      echo
      echo "    limactl shell $vm"
      echo "    cd $repo"
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
