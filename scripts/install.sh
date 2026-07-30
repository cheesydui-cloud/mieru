#!/usr/bin/env bash
# Unified one-liners:
#   # panel
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install.sh | bash -s -- panel
#
#   # agent
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install.sh | \
#     bash -s -- agent --panel-url http://x:8080 --node-id n_x --token tok_x --role exit
set -euo pipefail

BASE="https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts"
MODE="${1:-panel}"
shift || true

case "$MODE" in
  panel)
    curl -fsSL "${BASE}/install-panel.sh" | bash
    ;;
  agent)
    # pass remaining args
    curl -fsSL "${BASE}/install-agent.sh" | bash -s -- "$@"
    ;;
  *)
    echo "usage: install.sh panel | agent [agent-args...]" >&2
    exit 1
    ;;
esac
