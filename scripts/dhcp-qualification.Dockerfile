FROM golang:1.26.8-bookworm

ENV GOTOOLCHAIN=local GOFLAGS=-mod=readonly
RUN apt-get update && apt-get install -y --no-install-recommends \
    iproute2 busybox procps util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN sha256sum go.mod go.sum > /tmp/module-locks.sha256 \
    && go mod download && go mod verify \
    && sha256sum -c /tmp/module-locks.sha256
COPY . .
# Warm only the compilation cache. /bin/true replaces execution of test binaries.
# Tests themselves always execute in fresh --network none containers.
RUN for package in dhcp app config control admin webassets; do \
      GOPROXY=off GOSUMDB=off go test -p 1 -run '^$' -exec /bin/true \
        ./internal/$package || true; \
    done
ENV GOPROXY=off GOSUMDB=off
