.PHONY: build run test clean release

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -o bin/frugal ./cmd/frugal

run: build
	./bin/frugal

test:
	go test ./...

clean:
	rm -rf bin/ dist/

release: clean
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -o dist/frugal-darwin-arm64 ./cmd/frugal
	GOOS=darwin GOARCH=amd64 go build -o dist/frugal-darwin-amd64 ./cmd/frugal
	GOOS=linux  GOARCH=arm64 go build -o dist/frugal-linux-arm64  ./cmd/frugal
	GOOS=linux  GOARCH=amd64 go build -o dist/frugal-linux-amd64  ./cmd/frugal
	@echo "built $(VERSION) binaries in dist/"
