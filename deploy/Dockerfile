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
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
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
ARG SOURCE_DATE_EPOCH=unrecorded
RUN test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT" && \
    test "$(git show -s --format=%ct HEAD)" = "$SOURCE_DATE_EPOCH" && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH goreleaser build --snapshot --clean --single-target --skip=before --output /out/dimsum
RUN SOURCE_COMMIT=$SOURCE_COMMIT SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH python3 scripts/artifact-metadata.py /out/metadata

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/dimsum && chown 65532:65532 /run/dimsum && chmod 0700 /run/dimsum
COPY --from=build /out/dimsum /usr/bin/dimsum
COPY --from=build /out/metadata /usr/share/doc/dimsum/metadata
ARG SOURCE_COMMIT=unrecorded
LABEL org.opencontainers.image.revision=$SOURCE_COMMIT
USER 65532:65532
WORKDIR /var/lib/dimsum
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/dimsum"]
CMD ["serve", "-config", "/etc/dimsum/dimsum.yaml", "-state", "/var/lib/dimsum/recovery"]
