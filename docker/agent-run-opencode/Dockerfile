# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.5
FROM golang:${GO_VERSION}-bookworm AS agent-feedback-builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# anvil-hotline is owned by github.com/hazyforge/anvil-hotline (public).
ARG ANVIL_HOTLINE_VERSION=v0.1.0
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	mkdir -p /out \
	&& CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
		GOBIN=/out go install "github.com/hazyforge/anvil-hotline/cmd/anvil-hotline@${ANVIL_HOTLINE_VERSION}" \
	&& cp /out/anvil-hotline /out/anvil-agent-feedback

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ ./api/
COPY internal/ ./internal/
COPY cmd/anvil-agentctl/ ./cmd/anvil-agentctl/
RUN --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
	go build -trimpath -ldflags="-s -w" -o /out/anvil-agentctl ./cmd/anvil-agentctl

FROM debian:bookworm-slim

ARG AGENT_UID=10001
ARG AGENT_GID=10001
ARG OPENCODE_VERSION=1.18.3
ARG OPENCODE_AMD64_SHA256=949e2ab72af9fd2d2037957e590c78f5d657cf87683a0f7cf3156358af0a8ebc
ARG OPENCODE_ARM64_SHA256=da0a631174eba380b2a1d51f9d364fa3812da433e72743c72471d4b5da59c69d
ARG OPENCODE_LICENSE_SHA256=625f0f619133f89bbbb2abe37369613dfa1885eba1e50d02170deb62bb42cb6b
ARG KUBECTL_VERSION=v1.34.0

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		coreutils \
		curl \
		git \
		gnupg \
		jq \
		openssh-client \
		python3 \
		ripgrep \
		tar \
	&& mkdir -p -m 0755 /etc/apt/keyrings \
	&& curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends gh \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in \
		amd64) opencode_artifact=opencode-linux-x64-baseline.tar.gz; opencode_sha="${OPENCODE_AMD64_SHA256}" ;; \
		arm64) opencode_artifact=opencode-linux-arm64.tar.gz; opencode_sha="${OPENCODE_ARM64_SHA256}" ;; \
		*) echo "unsupported OpenCode architecture ${arch}" >&2; exit 1 ;; \
	esac \
	&& curl -fsSLo /tmp/opencode.tar.gz "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/${opencode_artifact}" \
	&& printf '%s  %s\n' "${opencode_sha}" /tmp/opencode.tar.gz | sha256sum -c - \
	&& tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode \
	&& chmod 0755 /usr/local/bin/opencode \
	&& mkdir -p /usr/share/doc/opencode \
	&& curl -fsSLo /usr/share/doc/opencode/LICENSE "https://raw.githubusercontent.com/anomalyco/opencode/v${OPENCODE_VERSION}/LICENSE" \
	&& printf '%s  %s\n' "${OPENCODE_LICENSE_SHA256}" /usr/share/doc/opencode/LICENSE | sha256sum -c - \
	&& rm -f /tmp/opencode.tar.gz \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& opencode --version \
	&& kubectl version --client=true \
	&& gh --version \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid "${AGENT_GID}" opencode \
	&& useradd --system --uid "${AGENT_UID}" --create-home --gid opencode --home-dir /home/opencode opencode \
	&& mkdir -p /workspace /opt/anvil/opencode /opt/anvil-agent-run \
	&& chown -R opencode:opencode /workspace /opt/anvil/opencode /home/opencode

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY docker/agent-run-common/github-auth.sh /opt/anvil-agent-run/lib/github-auth.sh
COPY docker/agent-run-common/repository-checkout.sh /opt/anvil-agent-run/lib/repository-checkout.sh
COPY docker/agent-run-opencode/args.sh /opt/anvil-agent-run/lib/opencode-args.sh
COPY docker/agent-run-opencode/entrypoint.sh /usr/local/bin/anvil-agent-run-opencode
COPY --from=agent-feedback-builder /out/anvil-hotline /usr/local/bin/anvil-hotline
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback
COPY --from=agent-feedback-builder /out/anvil-agentctl /usr/local/bin/anvil-agentctl

RUN chmod 0755 /usr/local/bin/anvil-agent-run-opencode \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-hotline \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0755 /usr/local/bin/anvil-agentctl \
	&& chmod 0644 /opt/anvil-agent-run/lib/github-auth.sh /opt/anvil-agent-run/lib/repository-checkout.sh /opt/anvil-agent-run/lib/opencode-args.sh \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV ANVIL_OPENCODE_HOME=/opt/anvil/opencode
ENV HOME=/opt/anvil/opencode/home
ENV XDG_DATA_HOME=/opt/anvil/opencode/data
ENV XDG_CONFIG_HOME=/opt/anvil/opencode/config
ENV XDG_CACHE_HOME=/opt/anvil/opencode/cache
ENV XDG_STATE_HOME=/opt/anvil/opencode/state
ENV OPENCODE_DISABLE_AUTOUPDATE=true
ENV ANVIL_AGENT_RUN_AGENTS_FILE=/opt/anvil-agent-run/AGENTS.md
ENV ANVIL_AGENT_RUN_STATUS_FILE=/tmp/anvil-agent-run-status/status.jsonl
ENV ANVIL_AGENT_RUN_STATUS_LOG_PREFIX=ANVIL_AGENT_RUN_STATUS_JSON=
ENV ANVIL_AGENT_RUN_STATUS_TOOL=anvil-agent-status
ENV ANVIL_AGENT_RUN_SKILLS_DIR=/opt/anvil-agent-run/skills
ENV ANVIL_AGENT_FEEDBACK_TOOL=anvil-hotline
ENV ANVIL_HOTLINE_TOOL=anvil-hotline
ENV ANVIL_OBSERVABILITY_TOOL=anvil-observability

USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-opencode"]
