#!/bin/bash
set -euxo pipefail
. "$(dirname "$0")/lib/guard.sh"
require_linux
# Run INSIDE daedal-kvm once build-host-kernel.sh has produced bzImage. Chains the
# remaining cross-compile steps: stage host modules onto the shared mount, then
# build the guest vmlinux. After this, install-pvm-kernel.sh runs in daedal-pvm.
bash /Users/dylanwong/daedal/scripts/stage-host-modules.sh
bash /Users/dylanwong/daedal/scripts/build-guest-kernel.sh
echo PVM_BUILD_PIPELINE_DONE
