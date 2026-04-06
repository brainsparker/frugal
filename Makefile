.PHONY: build run test clean

build:
	go build -o bin/frugal ./cmd/frugal

run: build
	./bin/frugal

test:
	go test ./...

clean:
	rm -rf bin/
