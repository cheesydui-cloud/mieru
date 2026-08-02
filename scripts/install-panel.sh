#!/usr/bin/env bash
# Linux 一键安装 / 升级 Panel
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
#   curl -fsSL ... | PANEL_ADMIN_PASS='密码' bash
#   curl -fsSL ... | MIERU_VERSION=v0.2.4 bash
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
# 默认拉 GitHub latest；可 MIERU_VERSION=v0.4.40 钉死
VERSION="${MIERU_VERSION:-}"
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

if [[ -z "$VERSION" || "$VERSION" == "latest" ]]; then
  tag=$(curl -fsSL --connect-timeout 10 \
    "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1) || true
  if [[ -z "$tag" ]]; then
    tag=$(curl -fsSL --connect-timeout 10 \
      "https://ghfast.top/https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
      | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1) || true
  fi
  VERSION="${tag:-v0.4.40}"
fi
echo "==> 目标版本 ${VERSION}"

TARGET="linux-${arch}"
ASSET="mieru-panel-${VERSION}-${TARGET}.tar.gz"
URLS=(
  "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  "https://ghfast.top/https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  "https://mirror.ghproxy.com/https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  "https://ghproxy.net/https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  "https://gitdl.cn/https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else
  command -v sudo >/dev/null 2>&1 || { echo "需要 root 或 sudo" >&2; exit 1; }
  SUDO="sudo"
fi

panel_port_from_env() {
  local port=8080
  if [[ -f /etc/mieru-panel.env ]]; then
    local l
    l=$($SUDO grep '^PANEL_LISTEN=' /etc/mieru-panel.env 2>/dev/null | head -1 | cut -d= -f2- || true)
    if [[ -n "$l" ]]; then
      port="${l##*:}"
      port="${port//[^0-9]/}"
    fi
  fi
  [[ -z "$port" ]] && port=8080
  echo "$port"
}

kill_by_port() {
  local port="$1"
  # 新装系统端口上可能没有任何进程；所有探测在 pipefail 下必须容忍空匹配
  if command -v fuser >/dev/null 2>&1; then
    $SUDO fuser -k "${port}/tcp" 2>/dev/null || true
  fi
  if command -v ss >/dev/null 2>&1; then
    local pids=""
    pids=$(ss -lntp 2>/dev/null | sed -n "s/.*:${port} .*pid=\\([0-9]*\\).*/\\1/p" | sort -u || true)
    if [[ -z "$pids" ]]; then
      pids=$(ss -lntp 2>/dev/null | grep -E ":${port}\\b" 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u || true)
    fi
    for pid in $pids; do
      [[ -n "$pid" ]] || continue
      $SUDO kill -9 "$pid" 2>/dev/null || true
    done
  fi
  if command -v lsof >/dev/null 2>&1; then
    $SUDO lsof -tiTCP:"${port}" -sTCP:LISTEN 2>/dev/null | xargs -r $SUDO kill -9 2>/dev/null || true
  fi
  return 0
}

kill_panel() {
  echo "==> 停止所有旧 panel 进程"
  if command -v systemctl >/dev/null 2>&1; then
    $SUDO systemctl stop mieru-panel 2>/dev/null || true
    $SUDO systemctl stop mieru-panel.service 2>/dev/null || true
  fi
  # by binary name / path (include bare "panel" started from INSTALL_DIR)
  $SUDO pkill -9 -x mieru-panel 2>/dev/null || true
  $SUDO pkill -9 -x panel 2>/dev/null || true
  $SUDO pkill -9 -f '/usr/local/bin/mieru-panel' 2>/dev/null || true
  $SUDO pkill -9 -f "${INSTALL_DIR}/panel" 2>/dev/null || true
  local port
  port=$(panel_port_from_env)
  kill_by_port "$port"
  sleep 1
  # second pass — catch respawn races
  kill_by_port "$port"
  sleep 1
}

listener_exe() {
  local port="$1"
  local pid=""
  if command -v ss >/dev/null 2>&1; then
    pid=$(ss -lntp 2>/dev/null | grep -E ":${port}\\b" 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | head -1 || true)
  fi
  if [[ -z "$pid" ]] && command -v lsof >/dev/null 2>&1; then
    pid=$($SUDO lsof -tiTCP:"${port}" -sTCP:LISTEN 2>/dev/null | head -1 || true)
  fi
  if [[ -n "$pid" && -r "/proc/${pid}/exe" ]]; then
    readlink -f "/proc/${pid}/exe" 2>/dev/null || true
    return 0
  fi
  echo ""
  return 0
}


# Verify tarball against release SHA256SUMS (optional if file missing / offline).
# Set MIERU_SKIP_CHECKSUM=1 to skip. Fail closed when sums file is available.
verify_release_sha256() {
  local asset="$1"  # basename
  local tgz="$2"    # local path
  if [[ "${MIERU_SKIP_CHECKSUM:-0}" == "1" ]]; then
    echo "==> 跳过 SHA256 校验 (MIERU_SKIP_CHECKSUM=1)"
    return 0
  fi
  local sums_urls=(
    "https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
    "https://ghfast.top/https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
    "https://mirror.ghproxy.com/https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
    "https://ghproxy.net/https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
    "https://gitdl.cn/https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
  )
  local sums_file="$TMP/SHA256SUMS"
  local got="" expect=""
  rm -f "$sums_file"
  for su in "${sums_urls[@]}"; do
    if curl -fsSL --connect-timeout 10 --retry 1 "$su" -o "$sums_file" 2>/dev/null; then
      if [[ -s "$sums_file" ]] && grep -qE '^[0-9a-fA-F]{64} ' "$sums_file" 2>/dev/null; then
        break
      fi
    fi
    rm -f "$sums_file"
  done
  if [[ ! -s "$sums_file" ]]; then
    echo "==> 未找到 SHA256SUMS（旧 release 或网络），跳过校验"
    return 0
  fi
  expect=$(grep -E "[[:space:]]${asset}$" "$sums_file" | head -1 | awk '{print $1}' || true)
  if [[ -z "$expect" ]]; then
    echo "==> SHA256SUMS 中无 ${asset}，跳过校验" >&2
    return 0
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tgz" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$tgz" | awk '{print $1}')
  else
    got=$(python3 -c "import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())" "$tgz")
  fi
  # bash 3.2 (mac) has no ${var,,}; use tr for portability
  got_l=$(printf '%s' "$got" | tr 'A-F' 'a-f')
  expect_l=$(printf '%s' "$expect" | tr 'A-F' 'a-f')
  if [[ "$got_l" != "$expect_l" ]]; then
    echo "错误: SHA256 校验失败" >&2
    echo "  文件: ${asset}" >&2
    echo "  期望: ${expect}" >&2
    echo "  实际: ${got}" >&2
    echo "  可能镜像缓存了旧包。请换镜像、清缓存，或 MIERU_SKIP_CHECKSUM=1（不推荐）" >&2
    exit 1
  fi
  echo "==> SHA256 校验通过 ${got:0:12}…"
}

DL_OK=0
for URL in "${URLS[@]}"; do
  echo "==> 下载 ${URL}"
  if curl -fL --connect-timeout 15 --retry 2 --retry-delay 1 "$URL" -o "$TMP/$ASSET"; then
    SIZE=$(wc -c <"$TMP/$ASSET" | tr -d ' ')
    if [[ "${SIZE:-0}" -ge 1000000 ]]; then
      DL_OK=1
      break
    fi
    echo "  文件过小 (${SIZE} bytes)，换镜像…"
  else
    echo "  失败，换镜像…"
  fi
done
if [[ "$DL_OK" -ne 1 ]]; then
  echo "错误: 所有镜像下载失败。请确认 release: https://github.com/${REPO}/releases/tag/${VERSION}" >&2
  exit 1
fi
SIZE=$(wc -c <"$TMP/$ASSET" | tr -d ' ')
echo "==> 下载完成 ${SIZE} bytes"
verify_release_sha256 "$ASSET" "$TMP/$ASSET"

	# 列出成员：忽略 macOS AppleDouble (._*) / xattr 警告（GNU tar 可能 exit≠0）
	LIST_FILE="$TMP/tar.list"
	set +e
	tar -tzf "$TMP/$ASSET" >"$LIST_FILE" 2>"$TMP/tar.list.err"
	set -e
	if ! awk -F/ '{print $NF}' "$LIST_FILE" | grep -qx 'panel'; then
	  echo "错误: 压缩包内没有名为 panel 的文件" >&2
	  echo "--- tar 列表 ---" >&2
	  cat "$LIST_FILE" >&2 || true
	  echo "--- tar 警告 ---" >&2
	  cat "$TMP/tar.list.err" >&2 || true
	  exit 1
	fi

	kill_panel

	echo "==> 安装到 ${INSTALL_DIR}"
	$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin" "$TMP/extract"
	$SUDO rm -f "${INSTALL_DIR}/panel" \
	  "${PREFIX}/bin/mieru-panel" \
	  /usr/bin/mieru-panel /bin/mieru-panel 2>/dev/null || true
	# 解压到临时目录；忽略 xattr 警告（旧 mac 打包兼容）
	set +e
	tar -xzf "$TMP/$ASSET" -C "$TMP/extract" --strip-components=1 2>"$TMP/tar.extract.err"
	TAR_EC=$?
	set -e
	# 选真实二进制：文件名 panel 且体积 >1MB（排除 ._panel 元数据）
	PANEL_SRC=""
	while IFS= read -r -d '' f; do
	  [[ "$(basename "$f")" == "panel" ]] || continue
	  psz=$(wc -c <"$f" | tr -d ' ')
	  if [[ "${psz:-0}" -gt 1000000 ]]; then
	    PANEL_SRC="$f"
	    break
	  fi
	done < <(find "$TMP/extract" -type f -name 'panel' -print0 2>/dev/null)
	if [[ -z "$PANEL_SRC" || ! -f "$PANEL_SRC" ]]; then
	  echo "错误: 解压后找不到 panel 二进制 (tar_exit=${TAR_EC})" >&2
	  find "$TMP/extract" -maxdepth 3 -type f -ls 2>/dev/null | head -40 >&2 || true
	  cat "$TMP/tar.extract.err" >&2 || true
	  exit 1
	fi
	$SUDO install -m 755 "$PANEL_SRC" "${INSTALL_DIR}/panel"
	$SUDO install -m 755 "${INSTALL_DIR}/panel" "${PREFIX}/bin/mieru-panel"
	AGENT_SRC=""
	while IFS= read -r -d '' f; do
	  [[ "$(basename "$f")" == "agent" ]] || continue
	  asz=$(wc -c <"$f" | tr -d ' ')
	  if [[ "${asz:-0}" -gt 1000000 ]]; then
	    AGENT_SRC="$f"
	    break
	  fi
	done < <(find "$TMP/extract" -type f -name 'agent' -print0 2>/dev/null)
	if [[ -n "$AGENT_SRC" ]]; then
	  $SUDO install -m 755 "$AGENT_SRC" "${PREFIX}/bin/mieru-agent" 2>/dev/null || true
	fi
	$SUDO rm -f "${INSTALL_DIR}/._panel" "${INSTALL_DIR}/._agent" 2>/dev/null || true
	sync || true
	if command -v cmp >/dev/null 2>&1; then
	  if ! cmp -s "${INSTALL_DIR}/panel" "${PREFIX}/bin/mieru-panel"; then
	    echo "错误: ${INSTALL_DIR}/panel 与 ${PREFIX}/bin/mieru-panel 不一致" >&2
	    exit 1
	  fi
	fi

# 校验二进制版本（panel --version）
BIN_VER=$("${PREFIX}/bin/mieru-panel" --version 2>/dev/null | tr -d '[:space:]' || true)
echo "==> 二进制版本: ${BIN_VER:-未知} （期望 ${VERSION}）"
if [[ -z "$BIN_VER" ]]; then
  echo "错误: mieru-panel --version 无输出" >&2
  file "${PREFIX}/bin/mieru-panel" >&2 || true
  exit 1
fi
if [[ "$BIN_VER" != "$VERSION" ]]; then
  echo "错误: 安装后的二进制版本是 ${BIN_VER}，不是 ${VERSION}。" >&2
  echo "  which: $(command -v mieru-panel 2>/dev/null || true)" >&2
  echo "  ls: $($SUDO ls -la "${PREFIX}/bin/mieru-panel" "${INSTALL_DIR}/panel" 2>/dev/null || true)" >&2
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
  echo "==> 升级模式：保留 ${ENV_FILE}（不改管理员密码 / JWT）"
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
  # kill again before start in case something respawned
  kill_panel
  $SUDO systemctl reset-failed mieru-panel 2>/dev/null || true
  $SUDO systemctl start mieru-panel
  sleep 2
  # if still not running, try restart
  if ! $SUDO systemctl is-active --quiet mieru-panel; then
    echo "==> 服务未 active，尝试 restart"
    $SUDO systemctl restart mieru-panel
    sleep 2
  fi
  $SUDO systemctl --no-pager --full status mieru-panel || true
else
  echo "==> 无 systemd，请手动："
  echo "    set -a; source ${ENV_FILE}; set +a; cd ${INSTALL_DIR}; mieru-panel"
fi

# 本机探测 — always 127.0.0.1:PORT (never concatenate host:port wrongly)
PORT="${LISTEN##*:}"
PORT="${PORT//[^0-9]/}"
[[ -z "$PORT" ]] && PORT=8080
CHECK_URL="http://127.0.0.1:${PORT}"
# wait for listen
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl -s --max-time 2 "${CHECK_URL}/api/version" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
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

# show which process holds the port + its exe
LISTEN_PROC=""
LISTEN_EXE=""
if command -v ss >/dev/null 2>&1; then
  LISTEN_PROC=$(ss -lntp 2>/dev/null | grep -E ":${PORT}\\b" | head -3 || true)
fi
LISTEN_EXE=$(listener_exe "$PORT")
EXPECTED_EXE=$(readlink -f "${PREFIX}/bin/mieru-panel" 2>/dev/null || echo "${PREFIX}/bin/mieru-panel")

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
echo " 监听进程 : ${LISTEN_PROC:-?}"
echo " 监听exe  : ${LISTEN_EXE:-?}"
echo " 服务     : systemctl status mieru-panel"
echo "============================================"

# 严格校验：跑起来的必须是目标版本
if ! echo "$VER_JSON" | grep -q "\"version\":\"${VERSION}\""; then
  echo "✗ 运行中版本不对！期望 ${VERSION}，实际 ${VER_JSON}" >&2
  echo "  诊断命令：" >&2
  echo "    which -a mieru-panel; mieru-panel --version" >&2
  echo "    systemctl cat mieru-panel" >&2
  echo "    systemctl status mieru-panel --no-pager" >&2
  echo "    journalctl -u mieru-panel -n 80 --no-pager" >&2
  echo "    ss -lntp | grep ${PORT}" >&2
  echo "    ls -la /usr/local/bin/mieru-panel /opt/mieru-panel/panel" >&2
  echo "    md5sum /usr/local/bin/mieru-panel /opt/mieru-panel/panel 2>/dev/null" >&2
  echo "    docker ps | grep -i mieru" >&2
  exit 1
fi

# listener exe should match installed path when we can resolve it
if [[ -n "$LISTEN_EXE" && -n "$EXPECTED_EXE" ]]; then
  if [[ "$LISTEN_EXE" != "$EXPECTED_EXE" && "$LISTEN_EXE" != "${INSTALL_DIR}/panel" ]]; then
    # allow if same inode via /proc path variants
    if ! cmp -s "$LISTEN_EXE" "${PREFIX}/bin/mieru-panel" 2>/dev/null; then
      echo "✗ 端口 ${PORT} 上的进程不是刚安装的二进制" >&2
      echo "  listening exe: ${LISTEN_EXE}" >&2
      echo "  expected:      ${EXPECTED_EXE}" >&2
      echo "  可能有 Docker / 手动启动的旧 panel 占端口" >&2
      exit 1
    fi
  fi
fi

echo "✓ 运行中版本正确: ${VERSION}"
echo
echo "浏览器请：无痕窗口 或 Ctrl/Cmd+Shift+R 强刷，并重新登录。"
exit 0
