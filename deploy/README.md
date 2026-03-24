# Deployment Scripts

Provisioning scripts for setting up new lplex boat servers.

## Quick Start

```bash
# One-liner install (latest version, can0, port 8089)
curl -fsSL https://raw.githubusercontent.com/sixfathoms/lplex/main/deploy/setup-boat.sh | sudo bash

# Custom install
sudo ./setup-boat.sh --version 0.2.0 --interface can0 --port 8089 --bitrate 250000
```

## Files

| File | Description |
|---|---|
| `setup-boat.sh` | Interactive bash script for provisioning a boat server. Installs the `.deb` package, configures SocketCAN, writes HOCON config, and starts the systemd service. |
| `cloud-init.yaml` | Cloud-init user-data for automated provisioning of VM/cloud instances. Same setup as the bash script but runs unattended on first boot. |

## What it does

1. Installs dependencies (`can-utils`, `jq`, `curl`)
2. Downloads and installs the lplex `.deb` package from GitHub Releases
3. Configures the CAN interface via systemd-networkd (250 kbps NMEA 2000)
4. Writes `/etc/lplex/lplex-server.conf` with sensible defaults
5. Creates the journal directory
6. Enables and starts the `lplex-server` systemd service

## Environment Variables

The bash script accepts CLI flags or environment variables:

| Variable | Flag | Default | Description |
|---|---|---|---|
| `LPLEX_VERSION` | `--version` | `latest` | Version to install |
| `LPLEX_INTERFACE` | `--interface` | `can0` | CAN interface name |
| `LPLEX_PORT` | `--port` | `8089` | HTTP API port |
| `LPLEX_BITRATE` | `--bitrate` | `250000` | CAN bus bitrate |
| `LPLEX_JOURNAL_DIR` | `--journal` | `/var/log/lplex` | Journal directory |

## Requirements

- Ubuntu 22.04+ or Debian 12+ (x86_64 or arm64)
- Root access (sudo)
- CAN hardware (USB-CAN adapter or built-in)
- Internet access (to download the .deb package)
