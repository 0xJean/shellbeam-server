FROM golang:1.25-bookworm AS build

RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download all
COPY . .
ARG SERVER_VERSION=0.1.0-dev
ARG VERSION_HASH
ENV CGO_ENABLED=1
RUN set -eu; \
    if [ -z "$VERSION_HASH" ]; then VERSION_HASH="$(git rev-parse HEAD)"; fi; \
    make build notices DIST=/out SERVER_VERSION="$SERVER_VERSION" VERSION_HASH="$VERSION_HASH"; \
    cp LICENSE NOTICE UPSTREAM.md README.md README_zh-cn.md /out/; \
    mkdir /out/docs; \
    cp docs/shellbeam-server.md docs/upstream.md /out/docs/

FROM build AS check
RUN go test ./... && go vet ./...

# Export this stage with --output type=local,dest=dist for release packaging.
FROM scratch AS artifacts
COPY --from=build /out/ /

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 shellbeam \
    && useradd --uid 10001 --gid shellbeam --no-create-home shellbeam \
    && mkdir -p /app/data \
    && chown -R shellbeam:shellbeam /app
WORKDIR /app
COPY --from=build /out/shellbeam-server /usr/local/bin/shellbeam-server
COPY --from=build /out/LICENSE /out/NOTICE /out/UPSTREAM.md /usr/share/doc/shellbeam-server/
COPY --from=build /out/THIRD_PARTY_NOTICES /usr/share/doc/shellbeam-server/THIRD_PARTY_NOTICES
ARG SERVER_VERSION=0.1.0-dev
LABEL org.opencontainers.image.title="ShellBeam Server" \
      org.opencontainers.image.description="Independent Komari-compatible monitoring backend for ShellBeam" \
      org.opencontainers.image.source="https://github.com/0xJean/shellbeam-server" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="$SERVER_VERSION"
ENV GIN_MODE=release \
    KOMARI_LISTEN=0.0.0.0:25774 \
    GODEBUG=disablethp=1
USER shellbeam:shellbeam
EXPOSE 25774
CMD ["shellbeam-server", "server"]
