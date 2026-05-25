# Talyvor Code agent — build and dev commands.
#
# `make build` produces a stripped binary for the host platform.
# `make build-all` cross-compiles for the four release targets.
# `make install` drops the binary in $GOPATH/bin for daily use.

.PHONY: build build-all install test vet serve run clean

VERSION ?= 0.1.0
LDFLAGS := -w -s -X main.version=$(VERSION)

build:
	go build -trimpath -ldflags="$(LDFLAGS)" \
	  -o bin/talyvor-code ./cmd/agent

build-all:
	GOOS=darwin  GOARCH=arm64  go build -trimpath \
	  -ldflags="$(LDFLAGS)" -o bin/talyvor-code-darwin-arm64 \
	  ./cmd/agent
	GOOS=darwin  GOARCH=amd64  go build -trimpath \
	  -ldflags="$(LDFLAGS)" -o bin/talyvor-code-darwin-amd64 \
	  ./cmd/agent
	GOOS=linux   GOARCH=amd64  go build -trimpath \
	  -ldflags="$(LDFLAGS)" -o bin/talyvor-code-linux-amd64 \
	  ./cmd/agent
	GOOS=windows GOARCH=amd64  go build -trimpath \
	  -ldflags="$(LDFLAGS)" \
	  -o bin/talyvor-code-windows-amd64.exe ./cmd/agent

install:
	go install -trimpath -ldflags="$(LDFLAGS)" ./cmd/agent

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Start the MCP server against the local Lens/Track/Docs stack.
serve:
	go run ./cmd/agent serve

# Run the `check` command against your local stack. Useful after
# editing config to verify everything still resolves.
run:
	go run ./cmd/agent check

clean:
	rm -rf bin/
