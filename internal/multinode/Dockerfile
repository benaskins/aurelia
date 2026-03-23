# Aurelia daemon image for multi-node integration tests.
# Lean build: no container driver, no GPU (linux/amd64 + linux/arm64).
# Config, certs, and service specs are mounted at runtime by the test harness.

FROM golang:1.26 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 go build -tags nocontainer,nogpu -ldflags "-s -w" \
    -o /out/aurelia ./cmd/aurelia/

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/aurelia /usr/local/bin/aurelia

RUN mkdir -p /etc/aurelia/tls /etc/aurelia/services /var/lib/aurelia

EXPOSE 9090

ENTRYPOINT ["aurelia", "daemon", "--api-addr", "0.0.0.0:9090"]
