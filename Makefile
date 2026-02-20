.PHONY: build test test-integration lint fmt clean

build:
	go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty)" -o aurelia ./cmd/aurelia/

test:
	go test ./...

test-integration:
	go test -tags integration ./...

lint:
	go vet ./...

fmt:
	go fmt ./...

clean:
	rm -f aurelia
