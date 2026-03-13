.PHONY: all proto generate build test lint clean

all: proto build

proto:
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

lint: generate
	golangci-lint run

clean:
	rm -f lplex-server lplex-cloud lplex
	rm -f pgn/pgn_gen.go pgn/helpers_gen.go pgn/schema.json
	rm -rf pgn/proto/
