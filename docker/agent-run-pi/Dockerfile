# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.5

FROM golang:${GO_VERSION}-bookworm AS agent-feedback-builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/anvil-agent-feedback/ ./cmd/anvil-agent-feedback/
COPY lib/agentfeedback/ ./lib/agentfeedback/
RUN --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
	go build -trimpath -ldflags="-s -w" -o /out/anvil-agent-feedback ./cmd/anvil-agent-feedback

FROM node:24-bookworm-slim

ARG AGENT_UID=10001
ARG AGENT_GID=10001
ARG KUBECTL_VERSION=v1.34.0
ARG PI_VERSION=0.80.6
ARG PI_XAI_OAUTH_VERSION=1.3.1

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		curl \
		git \
		gnupg \
		jq \
		openssh-client \
		python3 \
		ripgrep \
	&& mkdir -p -m 0755 /etc/apt/keyrings \
	&& curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends gh \
	&& npm install -g --ignore-scripts "@earendil-works/pi-coding-agent@${PI_VERSION}" "pi-xai-oauth@${PI_XAI_OAUTH_VERSION}" \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& pi --version \
	&& test -f "$(npm root -g)/pi-xai-oauth/package.json" \
	&& kubectl version --client=true \
	&& gh --version \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid "${AGENT_GID}" piagent \
	&& useradd --system --uid "${AGENT_UID}" --create-home --gid piagent --home-dir /home/piagent piagent \
	&& mkdir -p /workspace /opt/anvil/pi /opt/anvil-agent-run \
	&& chown -R piagent:piagent /workspace /opt/anvil/pi /home/piagent

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY docker/agent-run-common/github-auth.sh /opt/anvil-agent-run/lib/github-auth.sh
COPY docker/agent-run-pi/entrypoint.sh /usr/local/bin/anvil-agent-run-pi
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback

RUN chmod 0755 /usr/local/bin/anvil-agent-run-pi \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0644 /opt/anvil-agent-run/lib/github-auth.sh \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV ANVIL_PI_HOME=/opt/anvil/pi
ENV PI_CODING_AGENT_DIR=/opt/anvil/pi/agent
ENV PI_CODING_AGENT_SESSION_DIR=/opt/anvil/pi/sessions
ENV ANVIL_PI_XAI_EXTENSION=/usr/local/lib/node_modules/pi-xai-oauth/extensions/xai-oauth.ts
ENV ANVIL_AGENT_RUN_AGENTS_FILE=/opt/anvil-agent-run/AGENTS.md
ENV ANVIL_AGENT_RUN_STATUS_FILE=/tmp/anvil-agent-run-status/status.jsonl
ENV ANVIL_AGENT_RUN_STATUS_LOG_PREFIX=ANVIL_AGENT_RUN_STATUS_JSON=
ENV ANVIL_AGENT_RUN_STATUS_TOOL=anvil-agent-status
ENV ANVIL_AGENT_RUN_SKILLS_DIR=/opt/anvil-agent-run/skills
ENV ANVIL_AGENT_FEEDBACK_TOOL=anvil-agent-feedback
ENV ANVIL_OBSERVABILITY_TOOL=anvil-observability

USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-pi"]
