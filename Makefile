VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build install test lint clean

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/zcx ./cmd/zcx

install:
	go install -ldflags "-s -w -X main.version=$(VERSION)" ./cmd/zcx

test:
	go test -race ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ dist/
