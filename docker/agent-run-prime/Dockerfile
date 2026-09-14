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
ARG PRIME_VERSION=0.9.4
ARG PRIME_SHA256=b8d752a53d11a8c9a7580e1fb5fc24f7ce74ccad979c7e6e6aa8880fc3ad90b0

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
		python3-venv \
		fd-find \
		ripgrep \
	&& mkdir -p -m 0755 /etc/apt/keyrings \
	&& curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
	&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends gh \
	&& curl -fsSL "https://pub-728493de92a943e2a9b2d17b4719f318.r2.dev/releases/v${PRIME_VERSION}/prime-agent-${PRIME_VERSION}.tgz" -o /tmp/prime-agent.tgz \
	&& printf '%s  /tmp/prime-agent.tgz\n' "${PRIME_SHA256}" | sha256sum -c - \
	&& npm install -g --ignore-scripts /tmp/prime-agent.tgz \
	&& rm /tmp/prime-agent.tgz \
	&& arch="$(dpkg --print-architecture)" \
	&& case "${arch}" in amd64|arm64) ;; *) echo "unsupported kubectl architecture ${arch}" >&2; exit 1 ;; esac \
	&& curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" \
	&& chmod 0755 /usr/local/bin/kubectl \
	&& prime-agent --version \
	&& kubectl version --client=true \
	&& gh --version \
	&& apt-get clean \
	&& rm -rf /var/lib/apt/lists/*

# Prime's npm postinstall is opt-in and fail-open. Install its packaged runtime
# and bundled Python skills explicitly; fail the image build if bootstrapping fails.
COPY docker/agent-run-prime/install-kernel.py /tmp/install-prime-kernel.py
RUN python3 -m venv /opt/prime-kernel \
    && /opt/prime-kernel/bin/python /tmp/install-prime-kernel.py \
    && ln -s /usr/bin/fdfind /usr/local/bin/fd \
    && rm /tmp/install-prime-kernel.py

RUN groupadd --system --gid "${AGENT_GID}" primeagent \
	&& useradd --system --uid "${AGENT_UID}" --create-home --gid primeagent --home-dir /home/primeagent primeagent \
	&& mkdir -p /workspace /opt/anvil/prime /opt/anvil-agent-run \
	&& chown -R primeagent:primeagent /workspace /opt/anvil/prime /home/primeagent

COPY docker/agent-run-codex/AGENTS.md /opt/anvil-agent-run/AGENTS.md
COPY docker/agent-run-codex/static-prompts/ /opt/anvil-agent-run/static-prompts/
COPY docker/agent-run-codex/skills/ /opt/anvil-agent-run/skills/
COPY docker/agent-run-codex/anvil-agent-status /usr/local/bin/anvil-agent-status
COPY docker/agent-run-codex/anvil-observability /usr/local/bin/anvil-observability
COPY docker/agent-run-common/github-auth.sh /opt/anvil-agent-run/lib/github-auth.sh
COPY docker/agent-run-common/repository-checkout.sh /opt/anvil-agent-run/lib/repository-checkout.sh
COPY docker/agent-run-prime/entrypoint.sh /usr/local/bin/anvil-agent-run-prime
COPY --from=agent-feedback-builder /out/anvil-hotline /usr/local/bin/anvil-hotline
COPY --from=agent-feedback-builder /out/anvil-agent-feedback /usr/local/bin/anvil-agent-feedback
COPY --from=agent-feedback-builder /out/anvil-agentctl /usr/local/bin/anvil-agentctl

RUN chmod 0755 /usr/local/bin/anvil-agent-run-prime \
	&& chmod 0755 /usr/local/bin/anvil-agent-status \
	&& chmod 0755 /usr/local/bin/anvil-observability \
	&& chmod 0755 /usr/local/bin/anvil-hotline \
	&& chmod 0755 /usr/local/bin/anvil-agent-feedback \
	&& chmod 0755 /usr/local/bin/anvil-agentctl \
	&& chmod 0644 /opt/anvil-agent-run/lib/github-auth.sh /opt/anvil-agent-run/lib/repository-checkout.sh \
	&& chmod 0644 /opt/anvil-agent-run/AGENTS.md \
	&& find /opt/anvil-agent-run/static-prompts /opt/anvil-agent-run/skills -type f -name '*.md' -exec chmod 0644 {} +

ENV ANVIL_PRIME_HOME=/opt/anvil/prime
ENV PRIME_AGENT_KERNEL_PYTHON=/opt/prime-kernel/bin/python
ENV ANVIL_AGENT_RUN_AGENTS_FILE=/opt/anvil-agent-run/AGENTS.md
ENV ANVIL_AGENT_RUN_STATUS_FILE=/tmp/anvil-agent-run-status/status.jsonl
ENV ANVIL_AGENT_RUN_STATUS_LOG_PREFIX=ANVIL_AGENT_RUN_STATUS_JSON=
ENV ANVIL_AGENT_RUN_STATUS_TOOL=anvil-agent-status
ENV ANVIL_AGENT_RUN_SKILLS_DIR=/opt/anvil-agent-run/skills
ENV ANVIL_AGENT_FEEDBACK_TOOL=anvil-hotline
ENV ANVIL_HOTLINE_TOOL=anvil-hotline
ENV ANVIL_OBSERVABILITY_TOOL=anvil-observability

COPY docker/agent-run-prime/kernel-smoke.mjs /opt/anvil-agent-run/kernel-smoke.mjs
USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /workspace
RUN node /opt/anvil-agent-run/kernel-smoke.mjs

ENTRYPOINT ["/usr/local/bin/anvil-agent-run-prime"]
