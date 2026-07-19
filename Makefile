CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
IMAGE_PREFIX ?=
IMAGE_TAG ?= dev

.PHONY: generate manifests test verify verify-runner-contract build docker-build images image-checks helm-lint archive-postgres-integration chart-package release-publish kind-upgrade-e2e kind-e2e

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

build:
	go build ./cmd/anvil-agents ./cmd/anvil-agents-api ./cmd/anvil-agent-feedback ./cmd/anvil-agentctl

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

release-publish:
	./hack/publish-release.sh \
		--prefix "$${REGISTRY_PREFIX:?set REGISTRY_PREFIX, for example ghcr.io/hazyforge}" \
		--version "$${VERSION:?set VERSION, for example v0.1.1}"

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
	@bash -n hack/test-api-chart.sh
	@bash -n hack/test-archive-chart.sh
	@bash -n hack/test-archive-postgres.sh
	@bash -n hack/package-chart.sh
	@bash -n hack/package-chart_test.sh
	@bash -n hack/test-kind.sh
	@bash -n hack/test-kind-upgrade.sh
	@bash -n hack/test-runner-repository-checkout.sh
	@bash -n hack/stream-agent-run.sh
	@hack/build-images.sh --help >/dev/null
	@hack/build-images.sh --list >/dev/null
	@hack/publish-images.sh --help >/dev/null
	@hack/publish-release.sh --help >/dev/null
	@hack/package-chart.sh --help >/dev/null
	@hack/package-chart_test.sh
	@hack/build-images_test.sh
	@hack/publish-images_test.sh
	@hack/publish-release_test.sh
	@hack/test-runner-repository-checkout.sh
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
		rg -q '^run_tool_setup$$' "$$script"; \
	done
	@for dockerfile in docker/agent-run-*/Dockerfile; do \
		rg -q 'agent-run-common/repository-checkout.sh' "$$dockerfile"; \
	done

verify: manifests test build helm-lint verify-runner-contract
	@test -z "$$(rg -l 'github.com/hazyforge/anvil-primaris|github.com/hazyforge/anvil-primaris/lib/go/anvilhub' --glob '*.go' .)"
