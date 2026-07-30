#!/usr/bin/env bash
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install.sh | bash -s -- panel
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install.sh | bash -s -- agent --panel-url http://x:8080 --node-id n_x --token tok --role exit
set -euo pipefail
BASE="https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts"
MODE="${1:-panel}"
shift || true
case "$MODE" in
  panel) curl -fsSL "${BASE}/install-panel.sh" | bash ;;
  agent) curl -fsSL "${BASE}/install-agent.sh" | bash -s -- "$@" ;;
  *) echo "用法: install.sh panel | agent ..." >&2; exit 1 ;;
esac
