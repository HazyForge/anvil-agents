# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.6

FROM golang:${GO_VERSION}-bookworm AS agent-feedback-builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# anvil-hotline is owned by github.com/hazyforge/anvil-hotline (public).
ARG ANVIL_HOTLINE_VERSION=v0.2.0
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

FROM node:24-bookworm-slim

ARG AGENT_UID=10001
ARG AGENT_GID=10001
ARG KUBECTL_VERSION=v1.34.0
# Immutable release URLs and SHA512 values from Google's platform manifests.
ARG AGY_RELEASE=1.2.2-6061403484848128
ARG AGY_VERSION=1.2.2

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		curl \
		git \
		gnupg \
		jq \
		openssl \
		openssh-client \
		python3 \
		ripgrep \
	&& mkdir -p -m 0755 /etc/apt/keyrings \
	&& curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends gh \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

RUN arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in \
		amd64) platform=linux-x64; archive=cli_linux_x64.tar.gz; checksum=74342cf2a78b344392e573b638a648a6ad1f8e877f494b96e20f9c2b79158d5c423c40b2dcf788703362bb0a9150f09c707fde599d7557ce01c12208802a63cb ;; \
		arm64) platform=linux-arm; archive=cli_linux_arm64.tar.gz; checksum=a1645a30f36b767c7534c2f6a53e99a9bfade993267efcca715f7a45d797d47d6561df787e9d4a51a3bdfc9be855d49d23fa3f4b91b2c661fd17314050836048 ;; \
		*) echo "unsupported agy architecture ${arch}" >&2; exit 1 ;; \
	esac \
	&& curl -fsSL "https://storage.googleapis.com/antigravity-public/antigravity-cli/${AGY_RELEASE}/${platform}/${archive}" -o /tmp/agy.tar.gz \
	&& printf '%s  %s\n' "${checksum}" /tmp/agy.tar.gz | sha512sum --check --strict \
	&& tar -xzf /tmp/agy.tar.gz -C /usr/local/bin antigravity \
	&& mv /usr/local/bin/antigravity /usr/local/bin/agy \
	&& chmod 0755 /usr/local/bin/agy \
	&& test "$(AGY_CLI_DISABLE_AUTO_UPDATE=true agy --version)" = "${AGY_VERSION}" \
	&& rm /tmp/agy.tar.gz

RUN groupadd --system --gid "${AGENT_GID}" agy \
	&& useradd --system --uid "${AGENT_UID}" --create-home --gid agy --home-dir /home/agy agy \
	&& mkdir -p /workspace /opt/anvil/agy /opt/anvil-agent-run \
	&& chown -R agy:agy /workspace /opt/anvil/agy /home/agy

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY docker/agent-run-common/github-auth.sh /opt/anvil-agent-run/lib/github-auth.sh
COPY docker/agent-run-common/repository-checkout.sh /opt/anvil-agent-run/lib/repository-checkout.sh
COPY docker/agent-run-agy/turn.py /opt/anvil-agent-run/lib/agy-turn.py
COPY docker/agent-run-agy/entrypoint.sh /usr/local/bin/anvil-agent-run-agy
COPY --from=agent-feedback-builder /out/anvil-hotline /usr/local/bin/anvil-hotline
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback
COPY --from=agent-feedback-builder /out/anvil-agentctl /usr/local/bin/anvil-agentctl

RUN chmod 0755 /usr/local/bin/anvil-agent-run-agy \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-hotline \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0755 /usr/local/bin/anvil-agentctl \
	&& chmod 0644 /opt/anvil-agent-run/lib/github-auth.sh /opt/anvil-agent-run/lib/repository-checkout.sh \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV AGY_CLI_DISABLE_AUTO_UPDATE=true
ENV ANVIL_AGY_HOME=/opt/anvil/agy
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

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-agy"]
