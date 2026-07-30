#!/usr/bin/env bash
# Linux 一键安装 / 升级 Agent
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
#     bash -s -- --panel-url http://面板IP:8080 --node-id n_xxx --token tok_xxx --role exit
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.1.7}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
INSTALL_DIR="${MIERU_INSTALL_DIR:-/opt/mieru-panel}"
DATA_DIR="${MIERU_AGENT_DATA:-/var/lib/mieru-agent}"

PANEL_URL="${AGENT_PANEL_URL:-}"
NODE_ID="${AGENT_NODE_ID:-}"
TOKEN="${AGENT_TOKEN:-}"
ROLE="${AGENT_ROLE:-exit}"
NFT_DRYRUN="${AGENT_NFT_DRYRUN:-1}"

usage() {
  cat <<'EOF'
用法:
  bash install-agent.sh --panel-url URL --node-id ID --token TOKEN [--role exit|entry|relay]
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
    *) echo "未知参数: $1" >&2; usage; exit 1 ;;
  esac
done

if [[ -z "$PANEL_URL" || -z "$NODE_ID" || -z "$TOKEN" ]]; then
  echo "错误: 需要 --panel-url --node-id --token" >&2
  usage
  exit 1
fi

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "缺少命令: $1" >&2; exit 1; }; }
need_cmd curl
need_cmd tar
need_cmd uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
[[ "$os" == "linux" ]] || { echo "本脚本仅支持 Linux" >&2; exit 1; }

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "不支持的架构: $arch" >&2; exit 1 ;;
esac

ASSET="mieru-panel-${VERSION}-linux-${arch}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else
  command -v sudo >/dev/null 2>&1 || { echo "需要 root 或 sudo" >&2; exit 1; }
  SUDO="sudo"
fi

echo "==> 下载 ${URL}"
curl -fsSL "$URL" -o "$TMP/$ASSET"
$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin"
$SUDO tar -xzf "$TMP/$ASSET" -C "$INSTALL_DIR" --strip-components=1
$SUDO install -m 755 "$INSTALL_DIR/agent" "${PREFIX}/bin/mieru-agent"

ENV_FILE="/etc/mieru-agent.env"
echo "==> 写入 ${ENV_FILE}"
$SUDO tee "$ENV_FILE" >/dev/null <<EOF
AGENT_PANEL_URL=${PANEL_URL}
AGENT_NODE_ID=${NODE_ID}
AGENT_TOKEN=${TOKEN}
AGENT_ROLE=${ROLE}
AGENT_DATA=${DATA_DIR}
AGENT_NFT_DRYRUN=${NFT_DRYRUN}
EOF
$SUDO chmod 600 "$ENV_FILE"

if command -v systemctl >/dev/null 2>&1; then
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
  $SUDO systemctl enable mieru-agent >/dev/null 2>&1 || true
  $SUDO systemctl restart mieru-agent
  sleep 1
  $SUDO systemctl --no-pager --full status mieru-agent || true
fi

echo
echo "============================================"
echo " Agent 已安装/升级  ${VERSION}"
echo " role  : ${ROLE}"
echo " panel : ${PANEL_URL}"
echo " node  : ${NODE_ID}"
echo " env   : ${ENV_FILE}"
echo "============================================"
