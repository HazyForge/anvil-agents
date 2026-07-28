# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26.5
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-bookworm AS console
WORKDIR /console
COPY web/console/package.json web/console/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/console/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GO_LICENSES_VERSION=v2.0.1

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Embed the production console SPA into the API binary.
COPY --from=console /console/dist ./internal/runapi/consolefs/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install "github.com/google/go-licenses/v2@${GO_LICENSES_VERSION}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    /go/bin/go-licenses save ./cmd/anvil-agents ./cmd/anvil-agents-api \
    --save_path /out/licenses
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/anvil-agents ./cmd/anvil-agents && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/anvil-agents-api ./cmd/anvil-agents-api

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source=https://github.com/HazyForge/anvil-agents
COPY --from=build /out/anvil-agents /usr/local/bin/anvil-agents
COPY --from=build /out/anvil-agents-api /usr/local/bin/anvil-agents-api
COPY --from=build /out/licenses /usr/share/licenses/anvil-agents
EXPOSE 8080 8081 8082
ENTRYPOINT ["/usr/local/bin/anvil-agents"]
