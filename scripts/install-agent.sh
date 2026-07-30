#!/usr/bin/env bash
# One-line install:
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
#     bash -s -- --panel-url http://PANEL:8080 --node-id n_xxx --token tok_xxx --role exit
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.1.0}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
INSTALL_DIR="${MIERU_INSTALL_DIR:-/opt/mieru-panel}"
DATA_DIR="${MIERU_AGENT_DATA:-/var/lib/mieru-agent}"
SYSTEMD="${MIERU_SYSTEMD:-1}"

PANEL_URL="${AGENT_PANEL_URL:-}"
NODE_ID="${AGENT_NODE_ID:-}"
TOKEN="${AGENT_TOKEN:-}"
ROLE="${AGENT_ROLE:-exit}"
NFT_DRYRUN="${AGENT_NFT_DRYRUN:-1}"

usage() {
  cat <<'EOF'
Usage:
  install-agent.sh --panel-url URL --node-id ID --token TOKEN [--role exit|entry|relay|hybrid]

Env vars also work: AGENT_PANEL_URL, AGENT_NODE_ID, AGENT_TOKEN, AGENT_ROLE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --panel-url) PANEL_URL="$2"; shift 2 ;;
    --node-id) NODE_ID="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --role) ROLE="$2"; shift 2 ;;
    --nft-dryrun) NFT_DRYRUN="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 1 ;;
  esac
done

if [[ -z "$PANEL_URL" || -z "$NODE_ID" || -z "$TOKEN" ]]; then
  echo "error: --panel-url, --node-id, --token are required" >&2
  usage
  exit 1
fi

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }; }
need_cmd curl
need_cmd tar
need_cmd uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

TARGET="${os}-${arch}"
ASSET="mieru-panel-${VERSION}-${TARGET}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "==> downloading ${URL}"
curl -fsSL "$URL" -o "$TMP/$ASSET"

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else
  command -v sudo >/dev/null 2>&1 || { echo "need root or sudo" >&2; exit 1; }
  SUDO="sudo"
fi

$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin"
$SUDO tar -xzf "$TMP/$ASSET" -C "$INSTALL_DIR" --strip-components=1
$SUDO install -m 755 "$INSTALL_DIR/agent" "${PREFIX}/bin/mieru-agent"
# panel binary is also useful on nodes sometimes
$SUDO install -m 755 "$INSTALL_DIR/panel" "${PREFIX}/bin/mieru-panel" 2>/dev/null || true

ENV_FILE="/etc/mieru-agent.env"
echo "==> writing ${ENV_FILE}"
$SUDO tee "$ENV_FILE" >/dev/null <<EOF
AGENT_PANEL_URL=${PANEL_URL}
AGENT_NODE_ID=${NODE_ID}
AGENT_TOKEN=${TOKEN}
AGENT_ROLE=${ROLE}
AGENT_DATA=${DATA_DIR}
AGENT_NFT_DRYRUN=${NFT_DRYRUN}
EOF
$SUDO chmod 600 "$ENV_FILE"

if [[ "$SYSTEMD" == "1" ]] && command -v systemctl >/dev/null 2>&1 && [[ "$os" == "linux" ]]; then
  echo "==> installing systemd unit"
  $SUDO tee /etc/systemd/system/mieru-agent.service >/dev/null <<EOF
[Unit]
Description=Mieru Node Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${PREFIX}/bin/mieru-agent
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now mieru-agent
  echo "==> service started: systemctl status mieru-agent"
else
  echo "==> start manually:"
  echo "    set -a; source ${ENV_FILE}; set +a; mieru-agent"
fi

echo
echo "============================================"
echo " Mieru Agent installed"
echo "--------------------------------------------"
echo " binary : ${PREFIX}/bin/mieru-agent"
echo " role   : ${ROLE}"
echo " panel  : ${PANEL_URL}"
echo " node   : ${NODE_ID}"
echo " env    : ${ENV_FILE}"
echo "============================================"
