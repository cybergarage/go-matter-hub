BIN := bin/go-matter-hub
PKG := ./...

.PHONY: all web check-web build test run clean

all: build

# Build the web UI into web/dist, which the Go binary embeds.
web:
	cd web && npm ci && npm run build

check-web:
	cd web && npm ci && npm run check

build: web
	go build -o $(BIN) ./cmd/go-matter-hub

test:
	test -z "$$(gofmt -l .)"
	go vet $(PKG)
	go test -race -count=1 $(PKG)

run: build
	$(BIN)

clean:
	rm -rf bin web/node_modules
	find web/dist -mindepth 1 ! -name .gitkeep -delete
