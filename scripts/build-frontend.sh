#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../web"
npm run build
ls dist/index.html >/dev/null
echo "FRONTEND_BUILD_OK: web/dist/index.html"
