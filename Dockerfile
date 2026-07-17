# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25.5

FROM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/anvil-agents ./cmd/anvil-agents

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source=https://github.com/HazyForge/anvil-agents
COPY --from=build /out/anvil-agents /usr/local/bin/anvil-agents
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/anvil-agents"]
