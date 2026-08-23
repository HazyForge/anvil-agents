# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.6
ARG HERMES_AGENT_IMAGE=nousresearch/hermes-agent:v2026.7.7.2
FROM golang:${GO_VERSION}-bookworm AS agent-feedback-builder

ARG TARGETOS
ARG TARGETARCH

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

FROM ${HERMES_AGENT_IMAGE}

ARG CODEX_VERSION=0.142.5
ARG KUBECTL_VERSION=v1.34.0

USER root

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
	&& npm install -g "@openai/codex@${CODEX_VERSION}" \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& ln -sf /opt/hermes/bin/hermes /usr/local/bin/hermes \
	&& hermes --help >/dev/null \
	&& codex --version \
	&& kubectl version --client=true \
	&& gh --version \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

RUN mkdir -p /opt/anvil-agent-run /workspace /opt/anvil/hermes /opt/anvil/codex \
	&& chown -R hermes:hermes /workspace /opt/anvil

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY docker/agent-run-common/github-auth.sh /opt/anvil-agent-run/lib/github-auth.sh
COPY docker/agent-run-common/repository-checkout.sh /opt/anvil-agent-run/lib/repository-checkout.sh
COPY docker/agent-run-hermes/entrypoint.sh /usr/local/bin/anvil-agent-run-hermes
COPY docker/agent-run-hermes/anvil-hermes-query /usr/local/bin/anvil-hermes-query
COPY --from=agent-feedback-builder /out/anvil-hotline /usr/local/bin/anvil-hotline
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback
COPY --from=agent-feedback-builder /out/anvil-agentctl /usr/local/bin/anvil-agentctl

RUN chmod 0755 /usr/local/bin/anvil-agent-run-hermes \
	&& chmod 0755 /usr/local/bin/anvil-hermes-query \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-hotline \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0755 /usr/local/bin/anvil-agentctl \
	&& chmod 0644 /opt/anvil-agent-run/lib/github-auth.sh /opt/anvil-agent-run/lib/repository-checkout.sh \
	&& anvil-hermes-query --help >/dev/null \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV PATH=/opt/hermes/bin:/opt/hermes/.venv/bin:${PATH}
ENV HERMES_HOME=/opt/anvil/hermes
ENV CODEX_HOME=/opt/anvil/codex
ENV ANVIL_AGENT_RUN_AGENTS_FILE=/opt/anvil-agent-run/AGENTS.md
ENV ANVIL_AGENT_RUN_STATUS_FILE=/tmp/anvil-agent-run-status/status.jsonl
ENV ANVIL_AGENT_RUN_STATUS_LOG_PREFIX=ANVIL_AGENT_RUN_STATUS_JSON=
ENV ANVIL_AGENT_RUN_STATUS_TOOL=anvil-agent-status
ENV ANVIL_AGENT_RUN_SKILLS_DIR=/opt/anvil-agent-run/skills
ENV ANVIL_AGENT_FEEDBACK_TOOL=anvil-hotline
ENV ANVIL_HOTLINE_TOOL=anvil-hotline
ENV ANVIL_OBSERVABILITY_TOOL=anvil-observability

USER 10000:10000
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-hermes"]
