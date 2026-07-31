#!/usr/bin/env bash
# Linux 一键安装 / 升级 Panel
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
#   curl -fsSL ... | PANEL_ADMIN_PASS='密码' bash
#   curl -fsSL ... | MIERU_VERSION=v0.2.2 bash
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.2.2}"
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
# 校验包内有 panel
tar -tzf "$TMP/$ASSET" | head -5

# 关键停服务再换二进制，避免旧进程占住文件 / 热重启失败
if command -v systemctl >/dev/null 2>&1; then
  echo "==> 停止旧进程（若存在）"
  $SUDO systemctl stop mieru-panel 2>/dev/null || true
  # 兜底杀掉仍占用 :8080 的旧 panel
  $SUDO pkill -x mieru-panel 2>/dev/null || true
  $SUDO pkill -f '/usr/local/bin/mieru-panel' 2>/dev/null || true
  sleep 1
fi

echo "==> 安装到 ${INSTALL_DIR}"
$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin"
# 清空旧目录里的 panel/agent，避免残留
$SUDO rm -f "${INSTALL_DIR}/panel" "${INSTALL_DIR}/agent" "${PREFIX}/bin/mieru-panel" "${PREFIX}/bin/mieru-agent"
$SUDO tar -xzf "$TMP/$ASSET" -C "$INSTALL_DIR" --strip-components=1
$SUDO install -m 755 "$INSTALL_DIR/panel" "${PREFIX}/bin/mieru-panel"
$SUDO install -m 755 "$INSTALL_DIR/agent" "${PREFIX}/bin/mieru-agent"

# 校验二进制版本（panel --version）
BIN_VER=$("${PREFIX}/bin/mieru-panel" --version 2>/dev/null | tr -d '[:space:]' || true)
echo "==> 二进制版本: ${BIN_VER:-未知} （期望 ${VERSION}）"
if [[ -n "$BIN_VER" && "$BIN_VER" != "$VERSION" ]]; then
  echo "错误: 安装后的二进制版本是 ${BIN_VER}，不是 ${VERSION}。下载/解压可能失败。" >&2
  exit 1
fi

JWT_SECRET="${PANEL_JWT_SECRET:-$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')}"
ADMIN_USER="${PANEL_ADMIN_USER:-admin}"
ADMIN_PASS="${PANEL_ADMIN_PASS:-$(openssl rand -base64 12 2>/dev/null | tr -d '=+/' | cut -c1-14)}"
LISTEN="${PANEL_LISTEN:-:8080}"
ENV_FILE="/etc/mieru-panel.env"
UPGRADE=0

if [[ -f "$ENV_FILE" ]]; then
  UPGRADE=1
  echo "==> 升级模式：保留 ${ENV_FILE}（不改管理员密码）"
  if [[ -n "${PANEL_ADMIN_PASS:-}" ]]; then
    if $SUDO grep -q '^PANEL_ADMIN_PASS=' "$ENV_FILE"; then
      $SUDO sed -i "s|^PANEL_ADMIN_PASS=.*|PANEL_ADMIN_PASS=${PANEL_ADMIN_PASS}|" "$ENV_FILE"
    else
      echo "PANEL_ADMIN_PASS=${PANEL_ADMIN_PASS}" | $SUDO tee -a "$ENV_FILE" >/dev/null
    fi
    ADMIN_PASS="$PANEL_ADMIN_PASS"
  else
    ADMIN_PASS=$($SUDO grep '^PANEL_ADMIN_PASS=' "$ENV_FILE" | head -1 | cut -d= -f2- || true)
  fi
  if [[ -n "${PANEL_ADMIN_USER:-}" ]]; then
    if $SUDO grep -q '^PANEL_ADMIN_USER=' "$ENV_FILE"; then
      $SUDO sed -i "s|^PANEL_ADMIN_USER=.*|PANEL_ADMIN_USER=${PANEL_ADMIN_USER}|" "$ENV_FILE"
    else
      echo "PANEL_ADMIN_USER=${PANEL_ADMIN_USER}" | $SUDO tee -a "$ENV_FILE" >/dev/null
    fi
    ADMIN_USER="$PANEL_ADMIN_USER"
  else
    ADMIN_USER=$($SUDO grep '^PANEL_ADMIN_USER=' "$ENV_FILE" | head -1 | cut -d= -f2- || echo admin)
  fi
  LISTEN=$($SUDO grep '^PANEL_LISTEN=' "$ENV_FILE" | head -1 | cut -d= -f2- || echo ":8080")
  [[ -n "$ADMIN_USER" ]] || ADMIN_USER=admin
  [[ -n "$ADMIN_PASS" ]] || ADMIN_PASS="(见 ${ENV_FILE})"
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
  echo "==> 配置 systemd 并启动服务"
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
  sleep 2
  $SUDO systemctl --no-pager --full status mieru-panel || true
else
  echo "==> 无 systemd，请手动："
  echo "    set -a; source ${ENV_FILE}; set +a; cd ${INSTALL_DIR}; mieru-panel"
fi

# 本机探测
PORT="${LISTEN##*:}"
[[ "$LISTEN" == :* ]] && CHECK_URL="http://127.0.0.1:${PORT}" || CHECK_URL="http://127.0.0.1${LISTEN}"
VER_JSON=$(curl -s --max-time 5 "${CHECK_URL}/api/version" 2>/dev/null || echo "")
ROOT=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "${CHECK_URL}/" 2>/dev/null || echo "000")
INDEX_JS=$(curl -s --max-time 5 "${CHECK_URL}/" 2>/dev/null | grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' | head -1 || true)
LOGIN_OK="未测"
if [[ -n "${ADMIN_PASS}" && "${ADMIN_PASS}" != "(见 ${ENV_FILE})" ]]; then
  LOGIN_BODY=$(curl -s --max-time 5 -X POST "${CHECK_URL}/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" 2>/dev/null || true)
  if echo "$LOGIN_BODY" | grep -q '"token"'; then
    LOGIN_OK="OK"
  else
    LOGIN_OK="FAIL: ${LOGIN_BODY}"
  fi
fi

echo
echo "============================================"
if [[ "$UPGRADE" -eq 1 ]]; then
  echo " 升级完成  目标 ${VERSION}"
else
  echo " 安装完成  目标 ${VERSION}"
fi
echo "--------------------------------------------"
echo " 面板地址 : http://服务器IP:${PORT}"
echo " 管理员   : ${ADMIN_USER}"
echo " 密  码   : ${ADMIN_PASS}"
echo " 密码文件 : ${ENV_FILE}"
echo " 二进制   : ${PREFIX}/bin/mieru-panel  (${BIN_VER:-?})"
echo " 登录探测 : ${LOGIN_OK}"
echo " API版本  : ${VER_JSON:-无法连接}"
echo " 首页资源 : ${INDEX_JS:-?}"
echo " / 状态码 : HTTP ${ROOT}"
echo " 服务     : systemctl status mieru-panel"
echo "============================================"

# 严格校验：跑起来的必须是目标版本
if echo "$VER_JSON" | grep -q "\"version\":\"${VERSION}\""; then
  echo "✓ 运行中版本正确: ${VERSION}"
else
  echo "✗ 运行中版本不对！期望 ${VERSION}，实际 ${VER_JSON}" >&2
  echo "  请检查：" >&2
  echo "    which mieru-panel; mieru-panel --version" >&2
  echo "    systemctl cat mieru-panel" >&2
  echo "    journalctl -u mieru-panel -n 50 --no-pager" >&2
  echo "    ss -lntp | grep 8080" >&2
  exit 1
fi
