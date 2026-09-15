#!/usr/bin/env bash
# Offline runtime contract for a built Hermes image; no provider credentials.
set -euo pipefail
image="${1:?usage: image_test.sh <built-hermes-image>}"
docker run --rm --network none --read-only --tmpfs /tmp:rw,exec,size=512m \
  --entrypoint /bin/sh "${image}" -c '
set -eu
test "$(id -u)" = 10000
go version
scratch=$(mktemp -d /tmp/anvil-hermes-go.XXXXXXXX)
trap '\''rm -rf "$scratch"'\'' EXIT
export GOCACHE="$scratch/cache" GOPATH="$scratch/go" GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
cd "$scratch"
printf '\''package main\nimport "fmt"\nfunc main() { fmt.Print("HERMES_GO_TOOL_READY") }\n'\'' > main.go
go build -o "$scratch/tool" main.go
test "$("$scratch/tool")" = HERMES_GO_TOOL_READY
printf "Hermes non-root Go tool compilation passed\n"
'
