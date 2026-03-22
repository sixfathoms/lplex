.PHONY: all proto generate build test lint clean check-proto-tools check-lint-tools

# Minimum required versions (major.minor)
MIN_PROTOC_VERSION := 3
MIN_PROTOC_GEN_GO_VERSION := 1.28
MIN_PROTOC_GEN_GO_GRPC_VERSION := 1.2

define check_cmd
	@command -v $(1) >/dev/null 2>&1 || { echo "error: $(1) not found. $(2)"; exit 1; }
endef

all: proto build

check-go:
	@command -v go >/dev/null 2>&1 || { \
		echo "error: go not found (requires 1.25+)."; \
		case "$$(uname -s)" in \
			Darwin) echo "Install: brew install go" ;; \
			Linux)  echo "Install: sudo apt install -y golang-go  (or download from https://go.dev/dl/)" ;; \
			*)      echo "Install: https://go.dev/dl/" ;; \
		esac; \
		exit 1; \
	}

check-proto-tools:
	@command -v protoc >/dev/null 2>&1 || { \
		echo "error: protoc not found."; \
		case "$$(uname -s)" in \
			Darwin) echo "Install: brew install protobuf" ;; \
			Linux)  echo "Install: sudo apt install -y protobuf-compiler" ;; \
			*)      echo "Install: https://grpc.io/docs/protoc-installation/" ;; \
		esac; \
		exit 1; \
	}
	@command -v protoc-gen-go >/dev/null 2>&1 || go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@command -v protoc-gen-go >/dev/null 2>&1 || { \
		echo "error: protoc-gen-go not found in PATH after install."; \
		echo "Add Go bin directory to your PATH:"; \
		echo "  echo 'export PATH=\"\$$HOME/go/bin:\$$PATH\"' >> ~/.zshrc && source ~/.zshrc"; \
		exit 1; \
	}

check-lint-tools:
	$(call check_cmd,golangci-lint,Install: https://golangci-lint.run/welcome/install/)

proto: check-proto-tools
	protoc --go_out=. --go_opt=module=github.com/sixfathoms/lplex \
		--go-grpc_out=. --go-grpc_opt=module=github.com/sixfathoms/lplex \
		proto/replication/v1/replication.proto

generate:
	go generate ./pgn/...

build: generate
	go build -o lplex-server ./cmd/lplex-server
	go build -o lplex-cloud ./cmd/lplex-cloud
	go build -o lplex ./cmd/lplex

test: generate
	go test ./... -v -count=1

lint: generate check-lint-tools
	golangci-lint run

clean:
	rm -f lplex-server lplex-cloud lplex
	rm -f pgn/pgn_gen.go pgn/helpers_gen.go pgn/schema.json
	rm -rf pgn/proto/
