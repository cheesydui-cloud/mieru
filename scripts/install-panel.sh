#!/usr/bin/env bash
# One-line install:
#   curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
set -euo pipefail

REPO="${MIERU_REPO:-cheesydui-cloud/mieru}"
VERSION="${MIERU_VERSION:-v0.1.0}"
PREFIX="${MIERU_PREFIX:-/usr/local}"
INSTALL_DIR="${MIERU_INSTALL_DIR:-/opt/mieru-panel}"
DATA_DIR="${MIERU_DATA_DIR:-/var/lib/mieru-panel}"
SYSTEMD="${MIERU_SYSTEMD:-1}"

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }; }
need_cmd curl
need_cmd tar
need_cmd uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux)  ;;
  darwin) ;;
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

echo "==> installing to ${INSTALL_DIR}"
if [[ "$(id -u)" -eq 0 ]]; then
  SUDO=""
else
  if command -v sudo >/dev/null 2>&1; then SUDO="sudo"; else
    echo "need root or sudo" >&2; exit 1
  fi
fi

$SUDO mkdir -p "$INSTALL_DIR" "$DATA_DIR" "${PREFIX}/bin"
$SUDO tar -xzf "$TMP/$ASSET" -C "$INSTALL_DIR" --strip-components=1
$SUDO install -m 755 "$INSTALL_DIR/panel" "${PREFIX}/bin/mieru-panel"
$SUDO install -m 755 "$INSTALL_DIR/agent" "${PREFIX}/bin/mieru-agent"

# optional: fetch web dist from source archive if missing
if [[ ! -d "$INSTALL_DIR/web/dist" ]]; then
  echo "==> fetching frontend (web/dist) from source tag ${VERSION}"
  if command -v npm >/dev/null 2>&1; then
    curl -fsSL "https://github.com/${REPO}/archive/refs/tags/${VERSION}.tar.gz" -o "$TMP/src.tgz"
    mkdir -p "$TMP/src"
    tar -xzf "$TMP/src.tgz" -C "$TMP/src" --strip-components=1
    (cd "$TMP/src/web" && npm ci --silent && npm run build --silent)
    $SUDO mkdir -p "$INSTALL_DIR/web"
    $SUDO cp -a "$TMP/src/web/dist" "$INSTALL_DIR/web/"
  else
    echo "!! npm not found; panel UI needs ./web/dist under WorkingDirectory"
    echo "   later: clone repo, cd web && npm ci && npm run build, copy dist to ${INSTALL_DIR}/web/dist"
  fi
fi

JWT_SECRET="${PANEL_JWT_SECRET:-$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')}"
ADMIN_USER="${PANEL_ADMIN_USER:-admin}"
ADMIN_PASS="${PANEL_ADMIN_PASS:-$(openssl rand -base64 12 2>/dev/null | tr -d '=+/' | cut -c1-14)}"
LISTEN="${PANEL_LISTEN:-:8080}"

ENV_FILE="/etc/mieru-panel.env"
echo "==> writing ${ENV_FILE}"
$SUDO tee "$ENV_FILE" >/dev/null <<EOF
PANEL_LISTEN=${LISTEN}
PANEL_DB=${DATA_DIR}/panel.db
PANEL_DATA=${DATA_DIR}
PANEL_JWT_SECRET=${JWT_SECRET}
PANEL_ADMIN_USER=${ADMIN_USER}
PANEL_ADMIN_PASS=${ADMIN_PASS}
EOF
$SUDO chmod 600 "$ENV_FILE"

if [[ "$SYSTEMD" == "1" ]] && command -v systemctl >/dev/null 2>&1 && [[ "$os" == "linux" ]]; then
  echo "==> installing systemd unit"
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
  $SUDO systemctl enable --now mieru-panel
  echo "==> service started: systemctl status mieru-panel"
else
  echo "==> start manually:"
  echo "    set -a; source ${ENV_FILE}; set +a; cd ${INSTALL_DIR}; mieru-panel"
fi

echo
echo "============================================"
echo " Mieru Panel installed"
echo "--------------------------------------------"
echo " binary : ${PREFIX}/bin/mieru-panel"
echo " home   : ${INSTALL_DIR}"
echo " data   : ${DATA_DIR}"
echo " env    : ${ENV_FILE}"
echo " admin  : ${ADMIN_USER} / ${ADMIN_PASS}"
echo " listen : ${LISTEN}"
echo "============================================"
echo " open: http://<server-ip>${LISTEN/:/:}"
echo " save the admin password now."
