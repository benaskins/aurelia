# Multi-stage build for examples integration tests.
# Compiles aurelia + both example binaries + the test binary,
# then runs the test inside a slim container (no Docker-in-Docker).

FROM golang:1.26 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build aurelia (lean — no container driver or GPU needed inside the test container)
RUN go build -tags nocontainer,nogpu -ldflags "-s -w" -o /out/aurelia ./cmd/aurelia/

# Build example binaries
RUN go build -o /out/hello-static  ./examples/hello-static/
RUN go build -o /out/hello-dynamic ./examples/hello-dynamic/

# Compile the integration test binary
RUN go test -tags integration -c -o /out/examples.test ./examples/

# -----------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/aurelia        /usr/local/bin/aurelia
COPY --from=builder /out/hello-static   /usr/local/bin/hello-static
COPY --from=builder /out/hello-dynamic  /usr/local/bin/hello-dynamic
COPY --from=builder /out/examples.test  /usr/local/bin/examples.test

ENTRYPOINT ["/usr/local/bin/examples.test", "-test.v"]
