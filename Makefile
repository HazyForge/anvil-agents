CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
IMAGE_PREFIX ?=
IMAGE_TAG ?= dev
REGISTRY_PREFIX ?= ghcr.io/hazyforge
RELEASE_PLATFORM ?= linux/amd64
RELEASE_OUTPUT ?= dist
RELEASE_CHART_REGISTRY ?=
RELEASE_REPO ?= HazyForge/anvil-agents
RELEASE_DEPLOY_VALUES ?= .hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml
RELEASE_IMAGE_LOCK ?= $(RELEASE_OUTPUT)/images-$(VERSION).lock.tsv

.PHONY: generate manifests test verify verify-runner-contract security security-govulncheck security-gosec security-trivy security-release build console-build console-typecheck console-embed console-embed-restore docker-build images image-checks helm-lint archive-postgres-integration chart-package release-tag release-tag-push release-local release-publish release-github release-local-all release-pin-deploy release-primaris release-primaris-fast release-primaris-hot deploy-primaris judge-prerequisites judge-kind-e2e kind-upgrade-e2e kind-e2e

generate:
	$(CONTROLLER_GEN) object paths=./api/...

manifests: generate
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd/bases
	rm -rf charts/anvil-agents/crds
	{ printf '%s\n' '{{- if .Values.crds.install }}'; \
		for file in config/crd/bases/*.yaml; do \
			sed '/^  annotations:$$/a\    helm.sh/resource-policy: keep\
    argocd.argoproj.io/sync-options: Prune=false' "$$file"; \
		done; \
		printf '%s\n' '{{- end }}'; \
	} > charts/anvil-agents/templates/crds.yaml

test:
	go test ./...

# Local / Primaris security gate (mirrored in GitHub Actions and .hazyforge/tests.yaml).
# Public repo Actions minutes cover CodeQL/scorecard; this target is the release fail-closed path.
# Prefer the go.mod toolchain (go1.26.5+) so stdlib CVE scans match CI.
security-govulncheck:
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...
	go build -trimpath -o /tmp/anvil-agents-security-scan ./cmd/anvil-agents
	govulncheck -mode=binary /tmp/anvil-agents-security-scan
	rm -f /tmp/anvil-agents-security-scan

# gosec: fail on real issues. Exclude noisy false-positive classes that CodeQL
# already covers more carefully (G101 name matches, G104 Close errors, G115
# int32 conversions, intentional CLI/env path IO under G301/G304/G703).
security-gosec:
	@command -v gosec >/dev/null || go install github.com/securego/gosec/v2/cmd/gosec@latest
	gosec -quiet \
		-exclude=G101,G104,G115,G301,G304,G703 \
		-exclude-dir=web -exclude-dir=charts \
		./cmd/... ./internal/... ./api/... ./lib/...

# Source-level security only (fast). Used on PR unit loops.
security: security-govulncheck security-gosec

# Per-container Trivy image scan (builds all seven images by default).
# Primaris release + GHA security matrix use this for public/release evidence.
security-trivy:
	./hack/security-trivy-images.sh \
		$(if $(IMAGE_PREFIX),--prefix "$(IMAGE_PREFIX)",) \
		--tag "$(IMAGE_TAG)" \
		--platform "$(RELEASE_PLATFORM)"

# Full release security gate: source scans + every container Trivy scan.
# Primaris ApplicationRelease suite "security" and release-primaris full/fast.
security-release: security security-trivy

build:
	go build ./cmd/anvil-agents ./cmd/anvil-agents-api ./cmd/anvil-agentctl

# Build the Anvil Agents Console SPA into web/console/dist.
console-build:
	cd web/console && npm ci && npm run build

console-typecheck:
	cd web/console && npm ci && npm run typecheck

# Copy built SPA assets into the go:embed tree used by anvil-agents-api.
# Docker multi-stage builds do this automatically; use this for local binaries.
# WARNING: replaces committed stub files under internal/runapi/consolefs/dist.
# Restore the stub before committing with `make console-embed-restore`.
console-embed: console-build
	rm -rf internal/runapi/consolefs/dist
	mkdir -p internal/runapi/consolefs/dist
	cp -a web/console/dist/. internal/runapi/consolefs/dist/

# Restore the in-tree stub SPA so local console-embed output is not committed.
console-embed-restore:
	rm -rf internal/runapi/consolefs/dist
	mkdir -p internal/runapi/consolefs/dist
	cp -a internal/runapi/consolefs/stub/. internal/runapi/consolefs/dist/

docker-build:
	ANVIL_AGENTS_IMAGE_PREFIX="$(IMAGE_PREFIX)" ANVIL_AGENTS_IMAGE_TAG="$(IMAGE_TAG)" \
		./hack/build-images.sh --component controller

images:
	ANVIL_AGENTS_IMAGE_PREFIX="$(IMAGE_PREFIX)" ANVIL_AGENTS_IMAGE_TAG="$(IMAGE_TAG)" \
		./hack/build-images.sh

image-checks:
	./hack/build-images.sh --check

helm-lint:
	./hack/test-api-chart.sh
	./hack/test-archive-chart.sh

archive-postgres-integration:
	./hack/test-archive-postgres.sh

chart-package:
	./hack/package-chart.sh --version "$${VERSION:?set VERSION, for example VERSION=0.1.1}"

release-tag:
	./hack/ensure-release-tag.sh --version "$${VERSION:?set VERSION, for example v0.1.1}"

release-tag-push:
	./hack/ensure-release-tag.sh --version "$${VERSION:?set VERSION, for example v0.1.1}" --push

release-local:
	@set --; \
	if [ -n "$(RELEASE_CHART_REGISTRY)" ]; then set -- "$$@" --chart-registry "$(RELEASE_CHART_REGISTRY)"; fi; \
	if [ "$(RELEASE_SKIP_VERIFICATION)" = "true" ]; then set -- "$$@" --skip-verification; fi; \
	./hack/publish-release.sh \
		--prefix "$(REGISTRY_PREFIX)" \
		--version "$${VERSION:?set VERSION, for example v0.1.1}" \
		--platform "$(RELEASE_PLATFORM)" \
		--output "$(RELEASE_OUTPUT)" \
		"$$@"

release-publish: release-local

release-github:
	@set --; \
	if [ "$(RELEASE_UPDATE_EXISTING)" = "true" ]; then set -- "$$@" --update-existing; fi; \
	./hack/create-github-release.sh \
		--repo "$(RELEASE_REPO)" \
		--version "$${VERSION:?set VERSION, for example v0.1.1}" \
		--output "$(RELEASE_OUTPUT)" \
		--chart-registry "$(if $(RELEASE_CHART_REGISTRY),$(RELEASE_CHART_REGISTRY),oci://$(REGISTRY_PREFIX)/charts)" \
		"$$@"

release-local-all:
	$(MAKE) release-tag-push
	$(MAKE) release-local
	$(MAKE) release-github

release-pin-deploy:
	./hack/pin-deploy-values-from-lock.sh \
		--image-lock "$(RELEASE_IMAGE_LOCK)" \
		--values "$(RELEASE_DEPLOY_VALUES)"

# Local Docker → Anvil Primaris (no GitHub Actions). See docs/release-primaris.md.
# full: VERSION=vX.Y.Z make release-primaris
# fast: VERSION=vX.Y.Z make release-primaris-fast
# hot:  make release-primaris-hot   (controller only; allow dirty; deploys)
# apply: make deploy-primaris
release-primaris:
	./hack/release-primaris.sh --mode full --version "$${VERSION:?set VERSION, for example v0.1.14}" \
		--prefix "$(REGISTRY_PREFIX)" \
		$(if $(filter true,$(RELEASE_SKIP_VERIFICATION)),--skip-verification,) \
		$(if $(filter true,$(RELEASE_DEPLOY)),--deploy,) \
		$(if $(filter true,$(RELEASE_GITHUB)),--github,)

release-primaris-fast:
	./hack/release-primaris.sh --mode fast --version "$${VERSION:?set VERSION, for example v0.1.14}" \
		--prefix "$(REGISTRY_PREFIX)" \
		$(if $(filter true,$(RELEASE_DEPLOY)),--deploy,) \
		$(if $(filter true,$(RELEASE_GITHUB)),--github,)

release-primaris-hot:
	./hack/release-primaris.sh --mode hot --prefix "$(REGISTRY_PREFIX)" \
		--component "$(or $(COMPONENT),controller)" \
		--allow-dirty \
		$(if $(filter false,$(RELEASE_DEPLOY)),--no-deploy,)

deploy-primaris:
	./hack/deploy-primaris.sh --manifests \
		--values "$(RELEASE_DEPLOY_VALUES)" \
		$(if $(KUBE_CONTEXT),--context $(KUBE_CONTEXT),)

judge-prerequisites:
	./hack/install-judge-prerequisites.sh

judge-kind-e2e:
	./hack/test-judge-kind.sh

kind-upgrade-e2e:
	./hack/test-kind-upgrade.sh

kind-e2e: kind-upgrade-e2e
	./hack/test-kind.sh

verify-runner-contract:
	@bash -n hack/build-images.sh
	@bash -n hack/build-images_test.sh
	@bash -n hack/publish-images.sh
	@bash -n hack/publish-images_test.sh
	@bash -n hack/publish-release.sh
	@bash -n hack/publish-release_test.sh
	@bash -n hack/ensure-release-tag.sh
	@bash -n hack/create-github-release.sh
	@bash -n hack/pin-deploy-values-from-lock.sh
	@bash -n hack/local-release_test.sh
	@bash -n hack/deploy-primaris.sh
	@bash -n hack/release-primaris.sh
	@bash -n hack/release-primaris_test.sh
	@bash -n hack/test-api-chart.sh
	@bash -n hack/test-archive-chart.sh
	@bash -n hack/test-archive-postgres.sh
	@bash -n hack/package-chart.sh
	@bash -n hack/package-chart_test.sh
	@bash -n hack/test-kind.sh
	@bash -n hack/install-judge-prerequisites.sh
	@bash -n hack/install-judge-prerequisites_test.sh
	@bash -n hack/test-judge-kind.sh
	@bash -n hack/test-kind-upgrade.sh
	@bash -n hack/test-kind-upgrade-cleanup.sh
	@bash -n hack/test-runner-repository-checkout.sh
	@bash -n hack/test-opencode-runner.sh
	@bash -n hack/stream-agent-run.sh
	@hack/build-images.sh --help >/dev/null
	@hack/build-images.sh --list >/dev/null
	@hack/publish-images.sh --help >/dev/null
	@hack/publish-release.sh --help >/dev/null
	@hack/ensure-release-tag.sh --help >/dev/null
	@hack/create-github-release.sh --help >/dev/null
	@hack/pin-deploy-values-from-lock.sh --help >/dev/null
	@hack/deploy-primaris.sh --help >/dev/null
	@hack/release-primaris.sh --help >/dev/null
	@hack/package-chart.sh --help >/dev/null
	@hack/package-chart_test.sh
	@hack/install-judge-prerequisites.sh --help >/dev/null
	@hack/install-judge-prerequisites_test.sh
	@hack/test-judge-kind.sh --help >/dev/null
	@rg -q 'kind_sha256=a6875aaea358acf0ac07786b1a6755d08fd640f4c79b7a2e46681cc13f49a04b' hack/install-judge-prerequisites.sh
	@rg -q 'kubectl_sha256=4f6a959dcc5b702135f8354cc7109b542a2933c46b808b248a214c1f69f817ea' hack/install-judge-prerequisites.sh
	@rg -q 'helm_sha256=0a745198de24545d0055cd8414bc8d2ba10363ef5f5d38369ea1b399671cc083' hack/install-judge-prerequisites.sh
	@rg -q 'helm_binary_sha256=d9ae10babb2d90558f411daf4ecae818c32adef9d33b12f67e81e8a489947003' hack/install-judge-prerequisites.sh
	@rg -q 'kindest/node:v1.32.2@sha256:f226345927d7e348497136874b6d207e0b32cc52154ad8323129352923a3142f' hack/test-judge-kind.sh
	@rg -q 'KIND_EXPERIMENTAL_PROVIDER=docker' hack/test-judge-kind.sh
	@rg -q 'oci://ghcr.io/hazyforge/charts/anvil-agents' hack/test-judge-kind.sh
	@rg -q 'sha256:16a867c09b21287029797e43ba42cb633277ed1d3eb8d764dc3516f00a4c970c' hack/test-judge-kind.sh
	@rg -q 'workflow_dispatch:' .github/workflows/publish.yaml
	@if rg -q 'tags: \\["v\\*"\\]|on: *push|^  push:' .github/workflows/publish.yaml; then \
		echo "publish workflow must not auto-publish on tag push; use local make release or manual dispatch" >&2; \
		exit 1; \
	fi
	@rg -q 'publish-release\.sh' .github/workflows/publish.yaml
	@rg -q -- '--skip-verification' .github/workflows/publish.yaml
	@if rg -q 'package-chart\.sh' .github/workflows/publish.yaml; then \
		echo "publish workflow must use publish-release.sh so chart defaults come from the image lock" >&2; \
		exit 1; \
	fi
	@rg -q 'github.com/google/go-licenses/v2@' Dockerfile
	@rg -q '/usr/share/licenses/anvil-agents' Dockerfile
	@rg -q 'GROK_VERSION=0.2.103' docker/agent-run-grok-build/Dockerfile
	@rg -q '/usr/share/doc/grok-build/THIRD-PARTY-NOTICES' docker/agent-run-grok-build/Dockerfile
	@if rg -q 'docker build|build-images\.sh' hack/test-judge-kind.sh; then \
		echo "judge Kind test must not build images" >&2; \
		exit 1; \
	fi
	@hack/build-images_test.sh
	@hack/publish-images_test.sh
	@hack/publish-release_test.sh
	@hack/local-release_test.sh
	@hack/release-primaris_test.sh
	@hack/test-kind-upgrade-cleanup.sh
	@hack/test-runner-repository-checkout.sh
	@hack/test-opencode-runner.sh
	@hack/stream-agent-run.sh --help >/dev/null
	@if ANVIL_AGENTS_ACCESS_TOKEN=dummy hack/stream-agent-run.sh \
		--endpoint https://agents.example.com@127.0.0.1 \
		--namespace agents --run run-1 >/dev/null 2>&1; then \
		echo "stream helper accepted an endpoint containing userinfo" >&2; \
		exit 1; \
	fi
	@for script in docker/agent-run-*/entrypoint.sh; do \
		bash -n "$$script"; \
		rg -q 'ANVIL_AGENT_RUN_TOOL_SETUP_FILES' "$$script"; \
		rg -q 'ANVIL_AGENT_RUN_TOOLS_JSON' "$$script"; \
		rg -q 'repository-checkout.sh' "$$script"; \
		rg -q 'anvil_clone_repository_url' "$$script"; \
		rg -q '^run_tool_setup$$' "$$script"; \
	done
	@for dockerfile in docker/agent-run-*/Dockerfile; do \
		rg -q 'agent-run-common/repository-checkout.sh' "$$dockerfile"; \
	done

verify: manifests test build helm-lint verify-runner-contract console-typecheck
	@test -z "$$(rg -l 'github.com/hazyforge/anvil-primaris|github.com/hazyforge/anvil-primaris/lib/go/anvilhub' --glob '*.go' .)"
