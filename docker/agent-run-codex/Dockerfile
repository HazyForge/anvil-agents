# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.5
FROM golang:${GO_VERSION}-bookworm AS agent-feedback-builder

ARG TARGETOS
ARG TARGETARCH

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

FROM node:26-bookworm-slim

ARG AGENT_UID=10001
ARG AGENT_GID=10001
ARG CODEX_VERSION=0.142.5
ARG HELM_VERSION=v3.21.2
ARG KUBECTL_VERSION=v1.34.0

COPY --from=agent-feedback-builder /usr/local/go/ /usr/local/go/

ENV PATH=/usr/local/go/bin:${PATH}

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		curl \
		git \
		gnupg \
		jq \
		gzip \
		make \
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
	&& npm install -g "@openai/codex@${CODEX_VERSION}" \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& curl -fsSLo /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${arch}.tar.gz" \
	&& tar -xzf /tmp/helm.tar.gz -C /tmp \
	&& mv "/tmp/linux-${arch}/helm" /usr/local/bin/helm \
	&& chmod 0755 /usr/local/bin/helm \
	&& rm -rf /tmp/helm.tar.gz "/tmp/linux-${arch}" \
	&& codex --version \
	&& go version \
	&& test -x "$(go env GOROOT)/bin/gofmt" \
	&& make --version \
	&& helm version --short \
	&& kubectl version --client=true \
	&& gh --version \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

RUN ln -sf /usr/local/go/bin/go /usr/local/bin/go \
	&& ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
	&& /usr/local/bin/go version \
	&& test -x /usr/local/bin/gofmt

RUN groupadd --system --gid "${AGENT_GID}" codex \
	&& useradd --system --uid "${AGENT_UID}" --create-home --gid codex --home-dir /home/codex codex \
	&& mkdir -p /codex-home /workspace /opt/anvil-agent-run \
	&& chown -R codex:codex /codex-home /workspace /home/codex

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/goal-mode.md /opt/anvil-agent-run/goal-mode.md
COPY docker/agent-run-common/repository-checkout.sh /opt/anvil-agent-run/lib/repository-checkout.sh
COPY docker/agent-run-codex/entrypoint.sh /usr/local/bin/anvil-agent-run-codex
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY --from=agent-feedback-builder /out/anvil-hotline /usr/local/bin/anvil-hotline
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback
COPY --from=agent-feedback-builder /out/anvil-agentctl /usr/local/bin/anvil-agentctl

RUN chmod 0755 /usr/local/bin/anvil-agent-run-codex \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-hotline \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0755 /usr/local/bin/anvil-agentctl \
	&& chmod 0644 /opt/anvil-agent-run/lib/repository-checkout.sh \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md /opt/anvil-agent-run/goal-mode.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV CODEX_HOME=/codex-home
ENV ANVIL_AGENT_RUN_AGENTS_FILE=/opt/anvil-agent-run/AGENTS.md
ENV ANVIL_CODEX_GOAL_PROMPT_FILE=/opt/anvil-agent-run/goal-mode.md
ENV ANVIL_AGENT_RUN_STATUS_FILE=/tmp/anvil-agent-run-status/status.jsonl
ENV ANVIL_AGENT_RUN_STATUS_LOG_PREFIX=ANVIL_AGENT_RUN_STATUS_JSON=
ENV ANVIL_AGENT_RUN_STATUS_TOOL=anvil-agent-status
ENV ANVIL_AGENT_RUN_SKILLS_DIR=/opt/anvil-agent-run/skills
ENV ANVIL_AGENT_FEEDBACK_TOOL=anvil-hotline
ENV ANVIL_HOTLINE_TOOL=anvil-hotline
ENV ANVIL_OBSERVABILITY_TOOL=anvil-observability

USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-codex"]
