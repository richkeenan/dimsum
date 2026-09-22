# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24.21.0-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ghcr.io/goreleaser/goreleaser:v2.18.2 AS release-tool

FROM --platform=$BUILDPLATFORM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY --from=release-tool /usr/bin/goreleaser /usr/bin/goreleaser
COPY --from=web /usr/local/ /opt/node/
ENV PATH="/opt/node/bin:${PATH}"
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
COPY --from=web /src/web/node_modules ./web/node_modules
ARG TARGETOS
ARG TARGETARCH
ARG SOURCE_COMMIT=unrecorded
ARG SOURCE_DATE_EPOCH
RUN mkdir -p /out && \
    SOURCE_DATE="$(if [ -n "$SOURCE_DATE_EPOCH" ]; then date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ; else echo unrecorded; fi)" \
    SOURCE_COMMIT=$SOURCE_COMMIT GOOS=$TARGETOS GOARCH=$TARGETARCH goreleaser build --snapshot --clean --single-target --skip=before --output /out/dimsum
RUN SOURCE_COMMIT=$SOURCE_COMMIT SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH node scripts/artifact-metadata.mjs /out/metadata

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/dimsum && chown 65532:65532 /run/dimsum && chmod 0700 /run/dimsum
RUN install -d -o 65532 -g 65532 -m 0700 /etc/dimsum /etc/dimsum/secrets /var/lib/dimsum
COPY --chown=65532:65532 deploy/dimsum.example.yaml /etc/dimsum/dimsum.yaml
RUN chmod 0600 /etc/dimsum/dimsum.yaml
COPY --from=build /out/dimsum /usr/bin/dimsum
COPY --from=build /out/metadata /usr/share/doc/dimsum/metadata
ARG SOURCE_COMMIT=unrecorded
LABEL org.opencontainers.image.revision=$SOURCE_COMMIT
LABEL org.opencontainers.image.source=https://github.com/richkeenan/dimsum
USER 65532:65532
WORKDIR /var/lib/dimsum
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=6 CMD ["/usr/bin/dimsum", "control", "--socket", "/run/dimsum/control.sock", "request", "GET", "/health/ready"]
ENTRYPOINT ["/usr/bin/dimsum"]
CMD ["serve", "-config", "/etc/dimsum/dimsum.yaml", "-state", "/var/lib/dimsum/recovery"]
