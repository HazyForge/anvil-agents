CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
IMAGE ?= ghcr.io/hazyforge/anvil-agents:dev

.PHONY: generate manifests test verify verify-runner-contract build docker-build helm-lint

generate:
	$(CONTROLLER_GEN) object paths=./api/...

manifests: generate
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd/bases
	rm -rf charts/anvil-agents/crds
	{ printf '%s\n' '{{- if .Values.crds.install }}'; cat config/crd/bases/*.yaml; printf '%s\n' '{{- end }}'; } > charts/anvil-agents/templates/crds.yaml

test:
	go test ./...

build:
	go build ./cmd/anvil-agents ./cmd/anvil-agent-feedback

docker-build:
	docker build -f Dockerfile -t $(IMAGE) .

helm-lint:
	helm lint charts/anvil-agents
	helm template anvil-agents charts/anvil-agents >/dev/null
	helm template anvil-agents charts/anvil-agents --set crds.install=false >/dev/null

verify-runner-contract:
	@for script in docker/agent-run-*/entrypoint.sh; do \
		bash -n "$$script"; \
		rg -q 'ANVIL_AGENT_RUN_TOOL_SETUP_FILES' "$$script"; \
		rg -q 'ANVIL_AGENT_RUN_TOOLS_JSON' "$$script"; \
		rg -q '^run_tool_setup$$' "$$script"; \
	done

verify: manifests test build helm-lint verify-runner-contract
	@test -z "$$(rg -l 'github.com/hazyforge/anvil-primaris|github.com/hazyforge/anvil-primaris/lib/go/anvilhub' --glob '*.go' .)"
