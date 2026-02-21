version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

build:
    go build -ldflags "-X main.version={{version}}" -o aurelia ./cmd/aurelia/

test:
    go test ./...

test-integration:
    go test -tags integration ./...

lint:
    go vet ./...

fmt:
    go fmt ./...

build-lean:
    go build -tags nocontainer,nogpu -ldflags "-s -w -X main.version={{version}}" -o aurelia-lean ./cmd/aurelia/

clean:
    rm -f aurelia aurelia-lean

install-hooks:
    printf '#!/bin/sh\ngofmt -w .\ngit diff --quiet || { echo "gofmt reformatted files — re-stage and commit again"; exit 1; }\n' > .git/hooks/pre-commit
    chmod +x .git/hooks/pre-commit
