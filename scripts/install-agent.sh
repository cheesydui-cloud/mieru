#!/usr/bin/env bash
# Linux 一键安装 / 升级 Agent
#
# 首次安装（需要参数）:
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
#     bash -s -- --panel-url http://面板IP:8080 --node-id n_xxx --token tok_xxx --role exit
#
# 已安装后的固定升级（无参数，自动读 /etc/mieru-agent.env）:
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
#
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
# 默认跟随 GitHub latest；也可 MIERU_VERSION=v0.4.14 钉死版本
VERSION="${MIERU_VERSION:-}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
# Agent has its own install dir — never overwrite panel's /opt/mieru-panel
INSTALL_DIR="${MIERU_AGENT_INSTALL_DIR:-/opt/mieru-agent}"
DATA_DIR="${MIERU_AGENT_DATA:-/var/lib/mieru-agent}"
ENV_FILE="/etc/mieru-agent.env"

PANEL_URL="${AGENT_PANEL_URL:-}"
NODE_ID="${AGENT_NODE_ID:-}"
TOKEN="${AGENT_TOKEN:-}"
ROLE="${AGENT_ROLE:-}"
NFT_DRYRUN="${AGENT_NFT_DRYRUN:-1}"

usage() {
  cat <<'EOF'
用法:
  # 升级（已装过 agent，无参数）
  bash install-agent.sh

  # 首次安装
  bash install-agent.sh --panel-url URL --node-id ID --token TOKEN [--role exit|entry|relay|hybrid]

环境变量:
  MIERU_VERSION=v0.4.14   钉死版本（默认拉 GitHub latest）
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --panel-url) PANEL_URL="$2"; shift 2 ;;
    --node-id) NODE_ID="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --role) ROLE="$2"; shift 2 ;;
    --nft-dryrun) NFT_DRYRUN="$2"; shift 2 ;;
    --upgrade|-u) shift ;; # 兼容：显式升级，与无参相同
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数: $1" >&2; usage; exit 1 ;;
  esac
done

# 无参升级：从已有 env 读取
load_env_file() {
  local f="$1" line k v
  [[ -f "$f" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ "$line" =~ ^[A-Z0-9_]+= ]] || continue
    k="${line%%=*}"
    v="${line#*=}"
    case "$k" in
      AGENT_PANEL_URL) PANEL_URL="${PANEL_URL:-$v}" ;;
      AGENT_NODE_ID) NODE_ID="${NODE_ID:-$v}" ;;
      AGENT_TOKEN) TOKEN="${TOKEN:-$v}" ;;
      AGENT_ROLE) ROLE="${ROLE:-$v}" ;;
      AGENT_NFT_DRYRUN) NFT_DRYRUN="${NFT_DRYRUN:-$v}" ;;
      AGENT_DATA) DATA_DIR="${DATA_DIR:-$v}" ;;
    esac
  done < "$f"
  return 0
}

if [[ -z "$PANEL_URL" || -z "$NODE_ID" || -z "$TOKEN" ]]; then
  if load_env_file "$ENV_FILE"; then
    echo "==> 升级模式：从 ${ENV_FILE} 读取 node=${NODE_ID} role=${ROLE:-exit}"
  fi
fi

if [[ -z "$PANEL_URL" || -z "$NODE_ID" || -z "$TOKEN" ]]; then
  echo "错误: 需要 --panel-url --node-id --token（首次安装）" >&2
  echo "      或先装过一次，使 ${ENV_FILE} 存在后即可无参升级" >&2
  usage
  exit 1
fi
ROLE="${ROLE:-exit}"

# 解析版本：latest 或固定 tag
resolve_version() {
  if [[ -n "$VERSION" && "$VERSION" != "latest" ]]; then
    return 0
  fi
  local tag=""
  tag=$(curl -fsSL --connect-timeout 10 \
    "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1) || true
  if [[ -z "$tag" ]]; then
    # API 不通时用镜像/固定回退
    tag=$(curl -fsSL --connect-timeout 10 \
      "https://ghfast.top/https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
      | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1) || true
  fi
  if [[ -z "$tag" ]]; then
    tag="v0.4.14"
    echo "==> 无法查询 latest，回退 ${tag}"
  fi
  VERSION="$tag"
}
resolve_version
echo "==> 目标版本 ${VERSION}"

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
# Primary + CN-friendly mirrors (github.com often blocked on domestic VPS)
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

echo "==> 停止旧 Agent（如有）"
if command -v systemctl >/dev/null 2>&1; then
  $SUDO systemctl stop mieru-agent 2>/dev/null || true
fi
$SUDO pkill -9 -x mieru-agent 2>/dev/null || true
$SUDO pkill -9 -f '/usr/local/bin/mieru-agent' 2>/dev/null || true
$SUDO pkill -9 -f "${INSTALL_DIR}/agent" 2>/dev/null || true
sleep 1


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
  echo "错误: 所有镜像下载失败。可手动下载 ${ASSET} 后放到本机，或设置可访问 GitHub 的代理。" >&2
  exit 1
fi
SIZE=$(wc -c <"$TMP/$ASSET" | tr -d ' ')
verify_release_sha256 "$ASSET" "$TMP/$ASSET"

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

		# Fail closed if the binary we installed is not the requested tag.
		BIN_VER=$("${PREFIX}/bin/mieru-agent" -version 2>/dev/null | tr -d '[:space:]' || true)
		echo "==> 二进制版本: ${BIN_VER:-未知} （期望 ${VERSION}）"
		if [[ -z "$BIN_VER" ]]; then
		  echo "错误: 无法读取 ${PREFIX}/bin/mieru-agent -version" >&2
		  $SUDO ls -la "${PREFIX}/bin/mieru-agent" "${INSTALL_DIR}/agent" 2>/dev/null || true
		  exit 1
		fi
		# Accept v0.4.10 or 0.3.10
		want="${VERSION#v}"
		got="${BIN_VER#v}"
		if [[ "$got" != "$want" && "$BIN_VER" != "$VERSION" ]]; then
		  echo "错误: 安装后的 agent 版本是 ${BIN_VER}，不是 ${VERSION}。" >&2
		  echo "  多半是镜像缓存了旧包，或装到了错误路径。请用：" >&2
		  echo "  MIERU_VERSION=${VERSION} bash install-agent.sh ..." >&2
		  echo "  ls: $($SUDO ls -la "${PREFIX}/bin/mieru-agent" "${INSTALL_DIR}/agent" 2>/dev/null || true)" >&2
		  exit 1
		fi

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
