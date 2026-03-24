---
sidebar_position: 1
title: Installation
---

# Installation

lplex has three binaries with different platform support:

| Binary | Linux | macOS | Notes |
|---|---|---|---|
| `lplex-server` | amd64, arm64 | No | Requires SocketCAN |
| `lplex-cloud` | amd64, arm64 | No | Cloud receiver |
| `lplex` (client) | amd64, arm64 | amd64, arm64 | CLI tool only |

## Debian/Ubuntu (.deb package)

The `.deb` package bundles `lplex-server`, `lplex-cloud`, and `lplex` with a systemd unit file.

```bash
# Download the latest release
curl -LO https://github.com/sixfathoms/lplex/releases/latest/download/lplex_amd64.deb

# Install
sudo dpkg -i lplex_amd64.deb

# The service is not started automatically. Configure first:
sudo vim /etc/lplex-server/lplex-server.conf

# Then enable and start
sudo systemctl enable lplex-server
sudo systemctl start lplex-server
```

For ARM64 (Raspberry Pi, etc.):

```bash
curl -LO https://github.com/sixfathoms/lplex/releases/latest/download/lplex_arm64.deb
sudo dpkg -i lplex_arm64.deb
```

## Homebrew (lplex only)

The Homebrew formula installs only the `lplex` client. Available on macOS and Linux.

```bash
brew install sixfathoms/tap/lplex
```

## Docker

The Docker image runs on Linux (amd64 and arm64). It includes `lplex-server`, `lplex-cloud`, and the `lplex` client CLI.

```bash
# lplex-server (boat server)
docker run --rm --network=host \
  --device /dev/net/tun \
  ghcr.io/sixfathoms/lplex \
  lplex-server -interface can0 -port 8089

# lplex-cloud
docker run --rm -p 9443:9443 -p 8080:8080 \
  -v /data/lplex:/data/lplex \
  -v /etc/lplex-cloud:/etc/lplex-cloud:ro \
  ghcr.io/sixfathoms/lplex \
  lplex-cloud -data-dir /data/lplex

# Simulate a boat from recorded journal files (no CAN bus needed)
docker run --rm -p 8090:8090 \
  -v /path/to/journals:/data:ro \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex \
  simulate --dir /data --exit-when-done
```

:::note
The boat server needs `--network=host` to access the SocketCAN interface on the host.
:::

See [Simulation & Testing](/user-guide/simulation) for Docker Compose examples and integration testing patterns.

## Build from source

Requires Go 1.25 or later.

```bash
git clone https://github.com/sixfathoms/lplex.git
cd lplex

# Build all binaries
go build -o lplex-server ./cmd/lplex-server
go build -o lplex-cloud ./cmd/lplex-cloud
go build -o lplex ./cmd/lplex

# Run tests
go test ./... -v -count=1

# Lint
golangci-lint run
```

### Protobuf regeneration

Only needed if you modify `.proto` files:

```bash
# Install protoc plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

make proto
```

### PGN code generation

Only needed if you modify `.pgn` definition files:

```bash
go generate ./pgn/...
```
