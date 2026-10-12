# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go web/fallback.html ./web/
COPY --from=frontend /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /mantle-mask ./cmd/mantle-mask
RUN mkdir /empty-data

FROM gcr.io/distroless/static-debian12:nonroot
ARG SOURCE_URL=https://github.com/hnewcity/mantle-mask
LABEL org.opencontainers.image.title="Mantle Mask" \
      org.opencontainers.image.description="Self-hosted OpenAI-compatible gateway for Amazon Bedrock Mantle" \
      org.opencontainers.image.source=$SOURCE_URL \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=backend /mantle-mask /mantle-mask
COPY --from=backend --chown=65532:65532 /empty-data /app/data
COPY LICENSE NOTICE /usr/share/licenses/mantle-mask/
COPY web/src/components/ui/LICENSE.md /usr/share/licenses/mantle-mask/shadcn-ui-LICENSE
WORKDIR /app
ENV MANTLE_ADDR=0.0.0.0:8080 MANTLE_DATA_DIR=/app/data
EXPOSE 8080
VOLUME ["/app/data"]
USER 65532:65532
ENTRYPOINT ["/mantle-mask"]
