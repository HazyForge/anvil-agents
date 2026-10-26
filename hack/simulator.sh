#!/usr/bin/env bash
# Local Anvil Agents simulator: the real controller in Kind, the real API and
# console on loopback, a test-only OIDC issuer with one-click browser login,
# and credential-free simulated harnesses that play scripted agent work.
#
# TEST ONLY. The issuer auto-approves every login and the API trusts an http
# loopback issuer (allowInsecureIssuer). Never expose these ports.
#
#   hack/simulator.sh up       build, install, seed, start API + issuer
#   hack/simulator.sh wave     start another wave of seeded runs
#   hack/simulator.sh status   list simulated runs
#   hack/simulator.sh token    print an access token for curl or Desktop
#   hack/simulator.sh stop     stop the API and issuer (cluster keeps running)
#   hack/simulator.sh down     stop everything and delete the Kind cluster
#
# Environment:
#   ANVIL_AGENTS_SIM_CLUSTER   Kind cluster name (default anvil-agents-sim)
#   ANVIL_AGENTS_SIM_HOME      state directory (default <repo>/.sim)
#   ANVIL_AGENTS_SIM_API_PORT  API and console port (default 18180)
#   ANVIL_AGENTS_SIM_OIDC_PORT issuer port (default 18181)
#   ANVIL_AGENTS_SIM_SPEED     multiplier for simulated pauses (default 1)
#   ANVIL_AGENTS_SIM_SKIP_BUILD=1  reuse images and binaries from a previous up
#   ANVIL_AGENTS_SIM_CONSOLE_DIR   serve another console build (default web/console/dist)
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${ANVIL_AGENTS_SIM_CLUSTER:-anvil-agents-sim}"
kube_context="kind-${cluster_name}"
state_dir="${ANVIL_AGENTS_SIM_HOME:-${root_dir}/.sim}"
api_port="${ANVIL_AGENTS_SIM_API_PORT:-18180}"
oidc_port="${ANVIL_AGENTS_SIM_OIDC_PORT:-18181}"
sim_speed="${ANVIL_AGENTS_SIM_SPEED:-1}"
api_origin="http://127.0.0.1:${api_port}"
issuer="http://127.0.0.1:${oidc_port}"
client_id="anvil-agents-console-sim"
namespaces=(agents hazy-trade-agents)
# Launch controls authorize by application, not namespace. Keep this in step
# with the applicationRefs in examples/simulator/seed (hack/simulator_test.sh).
applications=(anvil-agents anvil-primaris hazy-social hazy-trade hazy-trade-strategies legacy-billing)
console_dir="${ANVIL_AGENTS_SIM_CONSOLE_DIR:-${root_dir}/web/console/dist}"
harness_image="anvil-agents-sim-harness:dev"

log() { printf '[simulator] %s\n' "$*" >&2; }
die() { printf '[simulator] %s\n' "$*" >&2; exit 1; }
kc() { kubectl --kubeconfig "${state_dir}/kubeconfig" --context "${kube_context}" "$@"; }

require() {
	local missing=()
	for command in "$@"; do
		command -v "${command}" >/dev/null 2>&1 || missing+=("${command}")
	done
	((${#missing[@]} == 0)) || die "missing tools: ${missing[*]}"
}

join_by() {
	local IFS="$1"
	shift
	echo "$*"
}

pid_alive() {
	local file="$1"
	[[ -f "${file}" ]] && kill -0 "$(cat "${file}")" 2>/dev/null
}

wait_http() {
	local url="$1" name="$2"
	for _ in $(seq 1 60); do
		curl -fsS --max-time 2 "${url}" >/dev/null 2>&1 && return 0
		sleep 1
	done
	die "${name} did not answer at ${url}; see ${state_dir}/${name}.log"
}

ensure_cluster() {
	if ! kind get clusters 2>/dev/null | grep -Fxq "${cluster_name}"; then
		log "creating Kind cluster ${cluster_name}"
		# Write the kubeconfig to the state directory only; the user's own
		# kubeconfig and current context stay untouched.
		kind create cluster --name "${cluster_name}" --kubeconfig "${state_dir}/kubeconfig" >/dev/null
	fi
	kind get kubeconfig --name "${cluster_name}" >"${state_dir}/kubeconfig"
	chmod 0600 "${state_dir}/kubeconfig"
}

build_artifacts() {
	if [[ "${ANVIL_AGENTS_SIM_SKIP_BUILD:-0}" == "1" ]]; then
		log "skipping builds (ANVIL_AGENTS_SIM_SKIP_BUILD=1)"
		return
	fi
	log "building controller image anvil-agents:dev"
	ANVIL_AGENTS_IMAGE_PREFIX="" "${root_dir}/hack/build-images.sh" --component controller --tag dev >&2
	log "building simulated harness image ${harness_image}"
	docker build --quiet --tag "${harness_image}" "${root_dir}/examples/simulator/harness" >&2
	log "building console assets"
	(cd "${root_dir}/web/console" && npm ci --no-audit --no-fund >&2 && npm run build >&2)
	log "building anvil-agents-api and kind-oidc-issuer"
	(cd "${root_dir}" && go build -buildvcs=false -o "${state_dir}/bin/anvil-agents-api" ./cmd/anvil-agents-api \
		&& go build -buildvcs=false -o "${state_dir}/bin/kind-oidc-issuer" ./cmd/kind-oidc-issuer)
}

install_controller() {
	log "loading images into Kind"
	kind load docker-image --name "${cluster_name}" anvil-agents:dev "${harness_image}" >&2
	log "installing the controller chart"
	helm --kubeconfig "${state_dir}/kubeconfig" --kube-context "${kube_context}" upgrade --install anvil-agents "${root_dir}/charts/anvil-agents" \
		--namespace anvil-agents-system --create-namespace \
		--set-string image.repository=anvil-agents \
		--set-string image.tag=dev \
		--set image.pullPolicy=Never \
		--set applicationMaxConcurrentRuns=3 \
		--wait --timeout 3m >&2
	kc rollout restart deployment --namespace anvil-agents-system --selector app.kubernetes.io/name=anvil-agents >/dev/null
	kc rollout status deployment --namespace anvil-agents-system --selector app.kubernetes.io/name=anvil-agents --timeout=3m >&2
}

seed_library() {
	log "applying the simulated library"
	kc apply --filename "${root_dir}/examples/simulator/seed/library.yaml" >&2
	if [[ "${sim_speed}" != "1" ]]; then
		for profile in $(kc get agentharnessprofiles --all-namespaces --output=jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}'); do
			kc patch agentharnessprofile "${profile#*/}" --namespace "${profile%%/*}" --type=json \
				--patch "[{\"op\":\"add\",\"path\":\"/spec/execution/extraEnv/-\",\"value\":{\"name\":\"SIM_SPEED\",\"value\":\"${sim_speed}\"}}]" >/dev/null
		done
	fi
}

start_wave() {
	log "starting a wave of simulated runs"
	kc create --filename "${root_dir}/examples/simulator/seed/runs.yaml" >&2
}

write_api_config() {
	local namespace_list application_list default_namespaces
	namespace_list="$(printf '        - %s\n' "${namespaces[@]}")"
	application_list="$(printf '        - %s\n' "${applications[@]}")"
	default_namespaces="$(printf '    - %s\n' "${namespaces[@]}")"
	cat >"${state_dir}/api-config.yaml" <<EOF
# Generated by hack/simulator.sh. TEST ONLY: http loopback issuer.
bindAddress: 127.0.0.1:${api_port}
oidc:
  issuer: ${issuer}
  audiences:
    - anvil-agents
  allowInsecureIssuer: true
authorization:
  bindings:
    - name: simulator-operator
      roles:
        - anvil-sim-operator
      permissions:
        - anvil-agents:runs:read
        - anvil-agents:runs:stream
        - anvil-agents:runs:create
        - anvil-agents:composition:read
        - anvil-agents:composition:write
      namespaces:
${namespace_list}
    # AgentRunControl checks the binding's namespaces against the application.
    - name: simulator-launch-controls
      roles:
        - anvil-sim-operator
      permissions:
        - anvil-agents:controls:read
        - anvil-agents:controls:write
      namespaces:
${application_list}
cors:
  allowedOrigins:
    - ${api_origin}
composition:
  readEnabled: true
  writeEnabled: true
controls:
  readEnabled: true
  writeEnabled: true
runs:
  createEnabled: true
ui:
  staticDir: ${console_dir}
  productTitle: Anvil Agents Simulator
  defaultNamespaces:
${default_namespaces}
  oidc:
    clientId: ${client_id}
EOF
}

start_host_processes() {
	mkdir -p "${state_dir}"
	if ! pid_alive "${state_dir}/issuer.pid"; then
		log "starting the test OIDC issuer on ${issuer}"
		nohup "${state_dir}/bin/kind-oidc-issuer" \
			--listen "127.0.0.1:${oidc_port}" \
			--issuer "${issuer}" \
			--key-file "${state_dir}/oidc-key.json" \
			--browser-login \
			--login-client-id "${client_id}" \
			--login-redirect-uri "${api_origin}/auth/callback" \
			--login-allowed-origin "${api_origin}" \
			--login-subject sim-operator \
			--login-email operator@simulator.local \
			--login-roles anvil-sim-operator \
			--login-namespaces "$(join_by , "${namespaces[@]}")" \
			--login-ttl 1h \
			>"${state_dir}/issuer.log" 2>&1 &
		echo $! >"${state_dir}/issuer.pid"
	fi
	wait_http "${issuer}/.well-known/openid-configuration" issuer
	write_api_config
	if ! pid_alive "${state_dir}/api.pid"; then
		log "starting anvil-agents-api on ${api_origin}"
		KUBECONFIG="${state_dir}/kubeconfig" nohup "${state_dir}/bin/anvil-agents-api" \
			--config "${state_dir}/api-config.yaml" \
			>"${state_dir}/api.log" 2>&1 &
		echo $! >"${state_dir}/api.pid"
	fi
	wait_http "${api_origin}/ui-config.json" api
}

stop_host_processes() {
	for name in api issuer; do
		if pid_alive "${state_dir}/${name}.pid"; then
			kill "$(cat "${state_dir}/${name}.pid")" 2>/dev/null || true
			log "stopped ${name}"
		fi
		rm -f "${state_dir}/${name}.pid"
	done
}

mint_token() {
	"${state_dir}/bin/kind-oidc-issuer" mint \
		--key-file "${state_dir}/oidc-key.json" \
		--issuer "${issuer}" --audience anvil-agents \
		--subject sim-operator --roles anvil-sim-operator \
		--namespaces "$(join_by , "${namespaces[@]}")"
}

cmd_up() {
	require docker kind kubectl helm go npm curl
	mkdir -p "${state_dir}/bin"
	chmod 0700 "${state_dir}"
	ensure_cluster
	build_artifacts
	install_controller
	seed_library
	start_host_processes
	if [[ -z "$(kc get agentruns --namespace agents --output=name 2>/dev/null)" ]]; then
		start_wave
	fi
	cat <<EOF

Anvil Agents simulator is up.
  Console   ${api_origin}/   (Sign in: one click, no password)
  Issuer    ${issuer}
  Logs      ${state_dir}/api.log, ${state_dir}/issuer.log
  kubectl   --kubeconfig ${state_dir}/kubeconfig --context ${kube_context}

More runs:  hack/simulator.sh wave
Stop:       hack/simulator.sh stop     Remove: hack/simulator.sh down
EOF
}

case "${1:-}" in
	up) cmd_up ;;
	wave) start_wave ;;
	status) kc get agentruns --all-namespaces --sort-by=.metadata.creationTimestamp \
		--output=custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,PROFILE:.spec.profileRef.name,PHASE:.status.phase,APP:.spec.scope.applicationRef.name ;;
	token) mint_token ;;
	stop) stop_host_processes ;;
	down)
		stop_host_processes
		kind delete cluster --name "${cluster_name}" >&2
		;;
	*)
		sed -n '2,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
		exit 2
		;;
esac
