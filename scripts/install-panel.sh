#!/usr/bin/env bash
# Linux 一键安装 / 升级 Panel
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
#   curl -fsSL ... | PANEL_ADMIN_PASS='密码' bash
#   curl -fsSL ... | MIERU_VERSION=v0.1.2 bash
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.1.3}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
INSTALL_DIR="${MIERU_INSTALL_DIR:-/opt/mieru-panel}"
DATA_DIR="${MIERU_DATA_DIR:-/var/lib/mieru-panel}"

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "缺少命令: $1" >&2; exit 1; }; }
need_cmd curl
need_cmd tar
need_cmd uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
if [[ "$os" != "linux" ]]; then
  echo "本脚本仅支持 Linux（当前: $os）" >&2
  exit 1
fi

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "不支持的架构: $arch（仅 linux-amd64 / linux-arm64）" >&2; exit 1 ;;
esac

TARGET="linux-${arch}"
ASSET="mieru-panel-${VERSION}-${TARGET}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else
  command -v sudo >/dev/null 2>&1 || { echo "需要 root 或 sudo" >&2; exit 1; }
  SUDO="sudo"
fi

echo "==> 下载 ${URL}"
curl -fsSL "$URL" -o "$TMP/$ASSET"

echo "==> 安装到 ${INSTALL_DIR}"
$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin"
$SUDO tar -xzf "$TMP/$ASSET" -C "$INSTALL_DIR" --strip-components=1
$SUDO install -m 755 "$INSTALL_DIR/panel" "${PREFIX}/bin/mieru-panel"
$SUDO install -m 755 "$INSTALL_DIR/agent" "${PREFIX}/bin/mieru-agent"

JWT_SECRET="${PANEL_JWT_SECRET:-$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')}"
ADMIN_USER="${PANEL_ADMIN_USER:-admin}"
ADMIN_PASS="${PANEL_ADMIN_PASS:-$(openssl rand -base64 12 2>/dev/null | tr -d '=+/' | cut -c1-14)}"
LISTEN="${PANEL_LISTEN:-:8080}"
ENV_FILE="/etc/mieru-panel.env"
UPGRADE=0

if [[ -f "$ENV_FILE" ]]; then
  UPGRADE=1
  echo "==> 升级模式：保留 ${ENV_FILE}（账号密码不变）"
  ADMIN_USER=$($SUDO grep '^PANEL_ADMIN_USER=' "$ENV_FILE" | head -1 | cut -d= -f2- || echo admin)
  ADMIN_PASS=$($SUDO grep '^PANEL_ADMIN_PASS=' "$ENV_FILE" | head -1 | cut -d= -f2- || echo "(见 env 文件)")
  LISTEN=$($SUDO grep '^PANEL_LISTEN=' "$ENV_FILE" | head -1 | cut -d= -f2- || echo ":8080")
else
  echo "==> 写入 ${ENV_FILE}"
  $SUDO tee "$ENV_FILE" >/dev/null <<EOF
PANEL_LISTEN=${LISTEN}
PANEL_DB=${DATA_DIR}/panel.db
PANEL_DATA=${DATA_DIR}
PANEL_JWT_SECRET=${JWT_SECRET}
PANEL_ADMIN_USER=${ADMIN_USER}
PANEL_ADMIN_PASS=${ADMIN_PASS}
EOF
  $SUDO chmod 600 "$ENV_FILE"
fi

if command -v systemctl >/dev/null 2>&1; then
  echo "==> 配置 systemd 并重启服务"
  $SUDO tee /etc/systemd/system/mieru-panel.service >/dev/null <<EOF
[Unit]
Description=Mieru Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${PREFIX}/bin/mieru-panel
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable mieru-panel >/dev/null 2>&1 || true
  $SUDO systemctl restart mieru-panel
  sleep 1
  $SUDO systemctl --no-pager --full status mieru-panel || true
else
  echo "==> 无 systemd，请手动："
  echo "    set -a; source ${ENV_FILE}; set +a; cd ${INSTALL_DIR}; mieru-panel"
fi

# 本机探测
PORT="${LISTEN##*:}"
[[ "$LISTEN" == :* ]] && CHECK_URL="http://127.0.0.1:${PORT}" || CHECK_URL="http://127.0.0.1${LISTEN}"
VER=$(curl -s --max-time 3 "${CHECK_URL}/api/version" 2>/dev/null || echo "无法连接")
ROOT=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "${CHECK_URL}/" 2>/dev/null || echo "000")

echo
echo "============================================"
if [[ "$UPGRADE" -eq 1 ]]; then
  echo " 升级完成  ${VERSION}"
else
  echo " 安装完成  ${VERSION}"
fi
echo "--------------------------------------------"
echo " 面板地址 : http://服务器IP:${PORT}"
echo " 管理员   : ${ADMIN_USER}"
echo " 密  码   : ${ADMIN_PASS}"
echo " 密码文件 : ${ENV_FILE}"
echo " 查看密码 : sudo cat ${ENV_FILE}"
echo " 服务状态 : systemctl status mieru-panel"
echo " 版本检查 : curl -s ${CHECK_URL}/api/version"
echo " 本机探测 : version=${VER}  / => HTTP ${ROOT}"
echo "============================================"
if [[ "$UPGRADE" -eq 0 ]]; then
  echo "请立即保存上方管理员密码。"
fi
