#!/usr/bin/env bash
# 重置管理员密码（Linux）
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/reset-admin.sh | bash
#   curl -fsSL ... | PANEL_ADMIN_PASS='新密码' bash
set -euo pipefail

ENV_FILE="${MIERU_ENV:-/etc/mieru-panel.env}"
BIN="${MIERU_PANEL_BIN:-/usr/local/bin/mieru-panel}"
NEW_PASS="${PANEL_ADMIN_PASS:-}"
NEW_USER="${PANEL_ADMIN_USER:-}"

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else
  command -v sudo >/dev/null 2>&1 || { echo "需要 root 或 sudo" >&2; exit 1; }
  SUDO="sudo"
fi

[[ -f "$ENV_FILE" ]] || { echo "找不到 $ENV_FILE，面板是否已安装？" >&2; exit 1; }
[[ -x "$BIN" ]] || { echo "找不到 $BIN" >&2; exit 1; }

# 可选：写入新密码到 env
if [[ -n "$NEW_PASS" ]]; then
  if $SUDO grep -q '^PANEL_ADMIN_PASS=' "$ENV_FILE"; then
    $SUDO sed -i "s|^PANEL_ADMIN_PASS=.*|PANEL_ADMIN_PASS=${NEW_PASS}|" "$ENV_FILE"
  else
    echo "PANEL_ADMIN_PASS=${NEW_PASS}" | $SUDO tee -a "$ENV_FILE" >/dev/null
  fi
fi
if [[ -n "$NEW_USER" ]]; then
  if $SUDO grep -q '^PANEL_ADMIN_USER=' "$ENV_FILE"; then
    $SUDO sed -i "s|^PANEL_ADMIN_USER=.*|PANEL_ADMIN_USER=${NEW_USER}|" "$ENV_FILE"
  else
    echo "PANEL_ADMIN_USER=${NEW_USER}" | $SUDO tee -a "$ENV_FILE" >/dev/null
  fi
fi

ADMIN_USER=$($SUDO grep '^PANEL_ADMIN_USER=' "$ENV_FILE" | head -1 | cut -d= -f2-)
ADMIN_PASS=$($SUDO grep '^PANEL_ADMIN_PASS=' "$ENV_FILE" | head -1 | cut -d= -f2-)
[[ -n "$ADMIN_USER" ]] || ADMIN_USER=admin
[[ -n "$ADMIN_PASS" ]] || { echo "env 里没有 PANEL_ADMIN_PASS" >&2; exit 1; }

echo "==> 停止服务"
$SUDO systemctl stop mieru-panel 2>/dev/null || true

echo "==> 同步数据库管理员密码"
# v0.1.3+ 支持 --reset-admin；旧版本则清空 admins 表让启动时重建
if $SUDO env -i PATH="$PATH" bash -c "set -a; . '$ENV_FILE'; set +a; '$BIN' --reset-admin" 2>/dev/null; then
  :
else
  echo "==> 当前二进制不支持 --reset-admin，改用清空 admins 表重建"
  DB=$($SUDO grep '^PANEL_DB=' "$ENV_FILE" | head -1 | cut -d= -f2-)
  [[ -n "$DB" ]] || DB=/var/lib/mieru-panel/panel.db
  if command -v sqlite3 >/dev/null 2>&1; then
    $SUDO sqlite3 "$DB" "DELETE FROM admins;"
  else
    # 无 sqlite3：直接删库会清空全部数据，给明确提示
    echo "未安装 sqlite3，无法只清 admins。请执行：" >&2
    echo "  sudo apt-get install -y sqlite3   # 或 yum install sqlite" >&2
    echo "  然后重新运行本脚本" >&2
    $SUDO systemctl start mieru-panel 2>/dev/null || true
    exit 1
  fi
fi

echo "==> 启动服务"
$SUDO systemctl start mieru-panel
sleep 1

echo
echo "============================================"
echo " 管理员密码已重置"
echo " 用户名 : ${ADMIN_USER}"
echo " 密  码 : ${ADMIN_PASS}"
echo " 来  源 : ${ENV_FILE}"
echo " 登录   : http://服务器IP:8080"
echo "============================================"
