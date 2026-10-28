# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=unknown DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/recipes ./cmd/recipes \
 && mkdir -p /out/data/media /out/data/backups /out/data/tmp

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="ninja-woodfire-recipe-keeper" \
      org.opencontainers.image.source="https://github.com/liorokman/ninjarecipe"
COPY --from=build /out/recipes /app/recipes
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_LISTEN_ADDR=":8080" \
    APP_DATA_DIR="/data" \
    TMPDIR="/data/tmp"
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/app/recipes","healthcheck"]
ENTRYPOINT ["/app/recipes"]
CMD ["serve"]
