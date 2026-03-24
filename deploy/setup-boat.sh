#!/usr/bin/env bash
#
# Provision a new lplex boat server on Ubuntu/Debian Linux.
# Run as root or with sudo.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/sixfathoms/lplex/main/deploy/setup-boat.sh | sudo bash
#   # or
#   sudo ./setup-boat.sh [--version 0.2.0] [--interface can0] [--port 8089]
#
set -euo pipefail

# --- Defaults ---
VERSION="${LPLEX_VERSION:-latest}"
CAN_IFACE="${LPLEX_INTERFACE:-can0}"
HTTP_PORT="${LPLEX_PORT:-8089}"
JOURNAL_DIR="${LPLEX_JOURNAL_DIR:-/var/log/lplex}"
BITRATE="${LPLEX_BITRATE:-250000}"

# --- Parse args ---
while [[ $# -gt 0 ]]; do
    case $1 in
        --version)   VERSION="$2"; shift 2;;
        --interface) CAN_IFACE="$2"; shift 2;;
        --port)      HTTP_PORT="$2"; shift 2;;
        --bitrate)   BITRATE="$2"; shift 2;;
        --journal)   JOURNAL_DIR="$2"; shift 2;;
        -h|--help)
            echo "Usage: setup-boat.sh [--version VER] [--interface can0] [--port 8089] [--bitrate 250000] [--journal /var/log/lplex]"
            exit 0;;
        *) echo "Unknown option: $1"; exit 1;;
    esac
done

echo "=== lplex boat server setup ==="
echo "  version:   ${VERSION}"
echo "  interface: ${CAN_IFACE}"
echo "  port:      ${HTTP_PORT}"
echo "  bitrate:   ${BITRATE}"
echo "  journal:   ${JOURNAL_DIR}"
echo

# --- Check root ---
if [[ $EUID -ne 0 ]]; then
    echo "Error: run as root (sudo)" >&2
    exit 1
fi

# --- Install dependencies ---
echo "--- Installing dependencies ---"
apt-get update -qq
apt-get install -y -qq can-utils jq curl > /dev/null

# --- Resolve version ---
if [[ "${VERSION}" == "latest" ]]; then
    VERSION=$(curl -s https://api.github.com/repos/sixfathoms/lplex/releases/latest | jq -r .tag_name | sed 's/^v//')
fi
ARCH=$(dpkg --print-architecture)
echo "--- Installing lplex ${VERSION} (${ARCH}) ---"

# --- Download and install .deb ---
DEB_URL="https://github.com/sixfathoms/lplex/releases/download/v${VERSION}/lplex_${VERSION}_linux_${ARCH}.deb"
curl -fsSL "${DEB_URL}" -o /tmp/lplex.deb
dpkg -i /tmp/lplex.deb
rm /tmp/lplex.deb
echo "  installed: $(lplex-server -version 2>&1 || echo 'lplex-server')"

# --- Configure CAN interface ---
echo "--- Configuring CAN interface (${CAN_IFACE} @ ${BITRATE} bps) ---"
mkdir -p /etc/systemd/network
cat > "/etc/systemd/network/80-${CAN_IFACE}.network" <<EOF
[Match]
Name=${CAN_IFACE}

[CAN]
BitRate=${BITRATE}
RestartSec=100ms
EOF
systemctl enable systemd-networkd
systemctl restart systemd-networkd

# --- Create journal directory ---
mkdir -p "${JOURNAL_DIR}"

# --- Write config ---
echo "--- Writing config to /etc/lplex/lplex-server.conf ---"
mkdir -p /etc/lplex
cat > /etc/lplex/lplex-server.conf <<EOF
interface = ${CAN_IFACE}
port = ${HTTP_PORT}

journal {
  dir = ${JOURNAL_DIR}
  prefix = nmea2k
  block-size = 262144
  compression = zstd
  rotate { duration = PT1H }
  retention {
    max-age = P30D
    min-keep = PT24H
  }
}

device { idle-timeout = 5m }
bus-silence-timeout = PT30S
ring-size = 65536
EOF

# --- Enable and start service ---
echo "--- Starting lplex-server ---"
systemctl daemon-reload
systemctl enable lplex-server
systemctl restart lplex-server

sleep 2
if systemctl is-active --quiet lplex-server; then
    echo
    echo "=== lplex boat server is running ==="
    echo "  HTTP API: http://$(hostname -I | awk '{print $1}'):${HTTP_PORT}"
    echo "  Service:  systemctl status lplex-server"
    echo "  Logs:     journalctl -u lplex-server -f"
    echo "  Journal:  ${JOURNAL_DIR}"
else
    echo
    echo "=== WARNING: lplex-server failed to start ==="
    systemctl status lplex-server --no-pager
    exit 1
fi
