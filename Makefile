.PHONY: build test dev clean

build:
	cd web && npm ci && npm run build
	go build -trimpath -o bin/mantle-mask ./cmd/mantle-mask

test:
	go test -race ./...
	go vet ./...
	cd web && npm ci && npm test && npm run typecheck

dev:
	go run ./cmd/mantle-mask

clean:
	go clean
