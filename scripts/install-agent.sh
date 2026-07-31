#!/usr/bin/env bash
# Linux 一键安装 / 升级 Agent
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
#     bash -s -- --panel-url http://面板IP:8080 --node-id n_xxx --token tok_xxx --role exit
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.2.5}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
# Agent has its own install dir — never overwrite panel's /opt/mieru-panel
INSTALL_DIR="${MIERU_AGENT_INSTALL_DIR:-/opt/mieru-agent}"
DATA_DIR="${MIERU_AGENT_DATA:-/var/lib/mieru-agent}"

PANEL_URL="${AGENT_PANEL_URL:-}"
NODE_ID="${AGENT_NODE_ID:-}"
TOKEN="${AGENT_TOKEN:-}"
ROLE="${AGENT_ROLE:-exit}"
NFT_DRYRUN="${AGENT_NFT_DRYRUN:-1}"

usage() {
  cat <<'EOF'
用法:
  bash install-agent.sh --panel-url URL --node-id ID --token TOKEN [--role exit|entry|relay|hybrid]
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

echo "==> 停止旧 Agent（如有）"
if command -v systemctl >/dev/null 2>&1; then
  $SUDO systemctl stop mieru-agent 2>/dev/null || true
fi
$SUDO pkill -9 -x mieru-agent 2>/dev/null || true
$SUDO pkill -9 -f '/usr/local/bin/mieru-agent' 2>/dev/null || true
$SUDO pkill -9 -f "${INSTALL_DIR}/agent" 2>/dev/null || true
sleep 1

echo "==> 下载 ${URL}"
if ! curl -fL --retry 3 --retry-delay 2 "$URL" -o "$TMP/$ASSET"; then
  echo "错误: 下载失败 ${URL}" >&2
  exit 1
fi
SIZE=$(wc -c <"$TMP/$ASSET" | tr -d ' ')
if [[ "${SIZE:-0}" -lt 1000000 ]]; then
  echo "错误: 下载文件过小 (${SIZE} bytes)" >&2
  exit 1
fi

	$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin" "$TMP/extract"
	# 忽略 macOS xattr / AppleDouble 警告
	set +e
	tar -xzf "$TMP/$ASSET" -C "$TMP/extract" --strip-components=1 2>"$TMP/tar.extract.err"
	set -e
	AGENT_SRC=""
	while IFS= read -r -d '' f; do
	  [[ "$(basename "$f")" == "agent" ]] || continue
	  asz=$(wc -c <"$f" | tr -d ' ')
	  if [[ "${asz:-0}" -gt 1000000 ]]; then
	    AGENT_SRC="$f"
	    break
	  fi
	done < <(find "$TMP/extract" -type f -name 'agent' -print0 2>/dev/null)
	if [[ -z "$AGENT_SRC" || ! -f "$AGENT_SRC" ]]; then
	  echo "错误: 压缩包内没有 agent 二进制" >&2
	  find "$TMP/extract" -maxdepth 3 -type f -ls 2>/dev/null | head -40 >&2 || true
	  cat "$TMP/tar.extract.err" 2>/dev/null >&2 || true
	  exit 1
	fi
	# Only install agent — never overwrite panel binary
	$SUDO install -m 755 "$AGENT_SRC" "${INSTALL_DIR}/agent"
	$SUDO install -m 755 "$AGENT_SRC" "${PREFIX}/bin/mieru-agent"
	sync || true

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
  sleep 2
  $SUDO systemctl --no-pager --full status mieru-agent || true
fi

# Connectivity probe from this node → panel
PANEL_URL="${PANEL_URL%/}"
echo "==> 探测面板连通性 ${PANEL_URL}"
HB_CODE=$(curl -s -o /tmp/mieru-hb.out -w "%{http_code}" --max-time 8 \
  -X POST "${PANEL_URL}/api/agent/heartbeat" \
  -H 'Content-Type: application/json' \
  -d "{\"node_id\":\"${NODE_ID}\",\"token\":\"${TOKEN}\",\"role\":\"${ROLE}\",\"agent_version\":\"${VERSION}\"}" \
  2>/dev/null || echo "000")
HB_BODY=$(cat /tmp/mieru-hb.out 2>/dev/null || true)
if [[ "$HB_CODE" == "200" ]]; then
  HB_RESULT="OK (HTTP 200) — 面板应显示 online"
elif [[ "$HB_CODE" == "401" ]]; then
  HB_RESULT="FAIL 401 unauthorized — node_id 或 token 不匹配，请重新复制安装命令"
elif [[ "$HB_CODE" == "000" ]]; then
  HB_RESULT="FAIL 无法连接面板 — 检查面板地址/防火墙/安全组是否放行 8080"
else
  HB_RESULT="FAIL HTTP ${HB_CODE} body=${HB_BODY}"
fi

ACTIVE="?"
if command -v systemctl >/dev/null 2>&1; then
  if $SUDO systemctl is-active --quiet mieru-agent; then
    ACTIVE="active"
  else
    ACTIVE="not-active"
  fi
fi

echo
echo "============================================"
echo " Agent 已安装/升级  ${VERSION}"
echo " role     : ${ROLE}"
echo " panel    : ${PANEL_URL}"
echo " node     : ${NODE_ID}"
echo " binary   : ${PREFIX}/bin/mieru-agent"
echo " install  : ${INSTALL_DIR}"
echo " env      : ${ENV_FILE}"
echo " 服务     : ${ACTIVE}"
echo " 心跳探测 : ${HB_RESULT}"
echo " 日志     : journalctl -u mieru-agent -f"
echo " 状态     : systemctl status mieru-agent"
echo "============================================"

if [[ "$ACTIVE" == "not-active" ]]; then
  echo "警告: mieru-agent 服务未 active，请查看 journalctl -u mieru-agent -n 50" >&2
  exit 1
fi
