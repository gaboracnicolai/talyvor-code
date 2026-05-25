# Talyvor Code agent — local commands.

.PHONY: build test vet run install

build:
	go build -trimpath -ldflags="-s -w" -o bin/talyvor-code ./cmd/agent

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Run the agent's `check` command against your local Lens + Track.
run:
	go run ./cmd/agent check

install:
	go install ./cmd/agent
