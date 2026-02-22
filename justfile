version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

build:
    go build -ldflags "-X main.version={{version}}" -o aurelia ./cmd/aurelia/

test:
    go test -short ./...

test-all:
    go test ./...

test-integration:
    go test -tags integration ./...

lint:
    go vet ./...

fmt:
    go fmt ./...

build-lean:
    go build -tags nocontainer,nogpu -ldflags "-s -w -X main.version={{version}}" -o aurelia-lean ./cmd/aurelia/

test-examples:
    docker build -f examples/Dockerfile -t aurelia-examples-test .
    docker run --rm aurelia-examples-test

install: build
    rm -f ~/.local/bin/aurelia
    mv aurelia ~/.local/bin/aurelia
    launchctl stop com.aurelia.daemon
    launchctl start com.aurelia.daemon
    @echo "Installed aurelia {{version}} and restarted daemon"

clean:
    rm -f aurelia aurelia-lean

install-hooks:
    printf '#!/bin/sh\ngofmt -w .\ngit diff --quiet || { echo "gofmt reformatted files — re-stage and commit again"; exit 1; }\n' > .git/hooks/pre-commit
    chmod +x .git/hooks/pre-commit

# Symlink skills from skills/ into .claude/skills/ for Claude Code discovery
install-skills:
    mkdir -p .claude/skills
    for dir in skills/*/; do \
        name=$(basename "$dir"); \
        ln -sfn "$(pwd)/$dir" ".claude/skills/$name"; \
    done
    @echo "Installed $(ls -1 skills/ | wc -l | tr -d ' ') skill(s) to .claude/skills/"
