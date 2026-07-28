#!/usr/bin/env bash
# Local Docker release path for Anvil Primaris — no GitHub Actions required.
#
# Modes:
#   full  Versioned seven-image release (verify + kind-e2e by default), pin
#         Primaris deploy.yaml, optional GitHub release page, optional deploy.
#   fast  Same as full but skips kind-e2e (still runs make verify unless
#         --skip-verification). Use for rapid console/API cutovers.
#   hot   Rebuild/push only selected components (default: controller), pin
#         those digests into the Primaris overlay, deploy immediately.
#         Best for "I changed the console / API / controller" loops.
#   deploy
#         Apply local chart + Primaris values (CRDs included) without building.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode=""
version=""
prefix="${REGISTRY_PREFIX:-ghcr.io/hazyforge}"
platform="${RELEASE_PLATFORM:-linux/amd64}"
output_dir="${RELEASE_OUTPUT:-${repo_root}/dist}"
values_file="${RELEASE_DEPLOY_VALUES:-${repo_root}/.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml}"
chart_registry=""
do_deploy=""
do_github=""
do_tag="true"
skip_verification="false"
allow_dirty="false"
kube_context=""
components=()
deploy_extra=()

usage() {
	cat <<'EOF'
Release Anvil Agents with local Docker and wire Anvil Primaris.

Usage:
  ./hack/release-primaris.sh --mode MODE [options]

Modes:
  full     VERSIONED release: tag at HEAD, verify+kind-e2e, build+push all 7
           images via Docker Buildx, package/push OCI chart, pin Primaris
           deploy.yaml digests. Optional --deploy / --github.
  fast     Like full but skips Kind e2e (still make verify). Intended for
           trusted local cutovers without GitHub Actions.
  hot      Quick controller (or component) cutover: docker build+push selected
           images, rewrite only those digests in Primaris deploy.yaml, then
           helm-deploy chart+CRDs to the current kube context.
  deploy   Only helm-upgrade the Primaris overlay from the local chart
           (crds.install from deploy.yaml). No image build.

GitHub Actions is never required. Log in to the registry with Docker (and Helm
for full/fast chart push) before running.

Options:
  --mode MODE              full | fast | hot | deploy
  --version vX.Y.Z         Required for full/fast. Optional for hot (defaults
                           to sha-<short> tags only).
  --prefix PREFIX          Registry prefix. Default: ghcr.io/hazyforge
  --platform PLATFORM      Default: linux/amd64
  --output DIR             dist/ for locks and chart packages
  --values FILE            Primaris deploy.yaml path
  --chart-registry URL     OCI chart dest for full/fast
  --component NAME         hot mode only; repeatable. Default: controller
  --deploy                 Helm-apply after publish/pin (default for hot/deploy)
  --no-deploy              Skip cluster apply (default for full/fast)
  --github                 Create GitHub Release page (full/fast only)
  --no-github              Do not create GitHub Release (default)
  --no-tag                 Do not create/push git tag (full/fast still require
                           an existing matching tag at HEAD unless hot)
  --skip-verification      Skip make verify and kind-e2e
  --allow-dirty            Permit hot publish from a dirty worktree
  --context CTX            kube-context for deploy
  --manifests              Regenerate CRDs before deploy
  -h, --help

Examples:
  # Full local release + pin Primaris overlay (GitOps commit afterward):
  ./hack/release-primaris.sh --mode full --version v0.1.14

  # Fast cutover without Kind e2e, push + pin + live deploy:
  ./hack/release-primaris.sh --mode fast --version v0.1.14 --deploy

  # Console-only iteration (controller image + live Primaris deploy):
  ./hack/release-primaris.sh --mode hot --component controller --allow-dirty

  # Apply current pins without rebuilding:
  ./hack/release-primaris.sh --mode deploy --manifests
EOF
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

image_name() {
	case "$1" in
		controller) printf '%s\n' anvil-agents ;;
		codex) printf '%s\n' anvil-agent-run-codex ;;
		opencode) printf '%s\n' anvil-agent-run-opencode ;;
		grok-build) printf '%s\n' anvil-agent-run-grok-build ;;
		hermes) printf '%s\n' anvil-agent-run-hermes ;;
		openclaw) printf '%s\n' anvil-agent-run-openclaw ;;
		pi) printf '%s\n' anvil-agent-run-pi ;;
		*) return 1 ;;
	esac
}

deploy_yaml_key_for_component() {
	# Map lock component → deploy.yaml field path key used by pin script / sed.
	case "$1" in
		controller) printf '%s\n' controller ;;
		codex) printf '%s\n' codex ;;
		opencode) printf '%s\n' openCode ;;
		grok-build) printf '%s\n' grokBuild ;;
		hermes) printf '%s\n' hermesAgent ;;
		openclaw) printf '%s\n' openClaw ;;
		pi) printf '%s\n' piAgent ;;
		*) return 1 ;;
	esac
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--mode)
			require_value "$1" "${2:-}"
			mode="$2"
			shift 2
			;;
		--version)
			require_value "$1" "${2:-}"
			version="$2"
			shift 2
			;;
		--prefix)
			require_value "$1" "${2:-}"
			prefix="${2%/}"
			shift 2
			;;
		--platform)
			require_value "$1" "${2:-}"
			platform="$2"
			shift 2
			;;
		--output)
			require_value "$1" "${2:-}"
			output_dir="$2"
			shift 2
			;;
		--values)
			require_value "$1" "${2:-}"
			values_file="$2"
			shift 2
			;;
		--chart-registry)
			require_value "$1" "${2:-}"
			chart_registry="${2%/}"
			shift 2
			;;
		--component)
			require_value "$1" "${2:-}"
			components+=("$2")
			shift 2
			;;
		--deploy)
			do_deploy="true"
			shift
			;;
		--no-deploy)
			do_deploy="false"
			shift
			;;
		--github)
			do_github="true"
			shift
			;;
		--no-github)
			do_github="false"
			shift
			;;
		--no-tag)
			do_tag="false"
			shift
			;;
		--skip-verification)
			skip_verification="true"
			shift
			;;
		--allow-dirty)
			allow_dirty="true"
			shift
			;;
		--context)
			require_value "$1" "${2:-}"
			kube_context="$2"
			shift 2
			;;
		--manifests)
			deploy_extra+=(--manifests)
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

[[ -n "${mode}" ]] || { echo "--mode is required" >&2; usage >&2; exit 2; }
case "${mode}" in
	full|fast|hot|deploy) ;;
	*)
		echo "unknown mode: ${mode}" >&2
		usage >&2
		exit 2
		;;
esac

# Default deploy policy by mode.
if [[ -z "${do_deploy}" ]]; then
	case "${mode}" in
		hot|deploy) do_deploy="true" ;;
		*) do_deploy="false" ;;
	esac
fi
if [[ -z "${do_github}" ]]; then
	do_github="false"
fi

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }

run_deploy() {
	local args=()
	if [[ -n "${kube_context}" ]]; then
		args+=(--context "${kube_context}")
	fi
	args+=(--values "${values_file}")
	if ((${#deploy_extra[@]})); then
		args+=("${deploy_extra[@]}")
	fi
	"${repo_root}/hack/deploy-primaris.sh" "${args[@]}"
}

pin_from_lock() {
	local lock="$1"
	"${repo_root}/hack/pin-deploy-values-from-lock.sh" \
		--image-lock "${lock}" \
		--values "${values_file}"
}

# Patch a single component digest into Primaris deploy.yaml without a full lock.
pin_component_ref() {
	local component="$1" immutable_ref="$2"
	local yaml_key
	yaml_key="$(deploy_yaml_key_for_component "${component}")"
	[[ -f "${values_file}" ]] || { echo "values file missing: ${values_file}" >&2; exit 2; }
	[[ "${immutable_ref}" == *@sha256:* ]] || { echo "not digest-pinned: ${immutable_ref}" >&2; exit 2; }

	local tmp
	tmp="$(mktemp "${values_file}.tmp.XXXXXX")"
	if [[ "${component}" == "controller" ]]; then
		awk -v ref="${immutable_ref}" '
			BEGIN { in_image=0 }
			/^image:/ { in_image=1; print; next }
			in_image && /^[[:space:]]+reference:/ {
				sub(/reference:[[:space:]].*/, "reference: " ref)
				print
				in_image=0
				next
			}
			/^[^[:space:]]/ { in_image=0 }
			{ print }
		' "${values_file}" > "${tmp}"
	else
		awk -v key="${yaml_key}" -v ref="${immutable_ref}" '
			BEGIN { in_runners=0 }
			/^runnerImages:/ { in_runners=1; print; next }
			in_runners && $0 ~ ("^[[:space:]]+" key ":") {
				sub(/:[[:space:]].*/, ": " ref)
				print
				next
			}
			/^[^[:space:]]/ && !/^runnerImages:/ { in_runners=0 }
			{ print }
		' "${values_file}" > "${tmp}"
	fi
	mv "${tmp}" "${values_file}"
	printf 'Pinned %s -> %s in %s\n' "${component}" "${immutable_ref}" "${values_file}"
}

case "${mode}" in
	deploy)
		run_deploy
		exit 0
		;;

	hot)
		if ((${#components[@]} == 0)); then
			components=(controller)
		fi
		if [[ "${allow_dirty}" != "true" ]]; then
			[[ -z "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]] || {
				echo "dirty worktree; pass --allow-dirty for hot mode" >&2
				exit 2
			}
		fi
		revision="$(git -C "${repo_root}" rev-parse HEAD)"
		short_revision="$(git -C "${repo_root}" rev-parse --short=7 HEAD)"
		sha_tag="sha-${short_revision}"
		tags=("${sha_tag}")
		if [[ -n "${version}" ]]; then
			tags+=("${version}")
		fi

		build_args=(
			--prefix "${prefix}"
			--platform "${platform}"
			--push
		)
		if [[ "${allow_dirty}" == "true" ]]; then
			build_args+=(--allow-dirty)
		fi
		for component in "${components[@]}"; do
			build_args+=(--component "${component}")
		done
		for tag in "${tags[@]}"; do
			build_args+=(--tag "${tag}")
		done

		echo "Hot-building components: ${components[*]} tags: ${tags[*]}"
		"${repo_root}/hack/build-images.sh" "${build_args[@]}"

		for component in "${components[@]}"; do
			repository="${prefix}/$(image_name "${component}")"
			digest="$(docker buildx imagetools inspect "${repository}:${sha_tag}" --format '{{.Manifest.Digest}}')"
			[[ "${digest}" == sha256:* ]] || { echo "failed to resolve digest for ${repository}:${sha_tag}" >&2; exit 1; }
			pin_component_ref "${component}" "${repository}@${digest}"
		done

		# Always regenerate CRDs on hot path when API may have changed.
		deploy_extra+=(--manifests)
		if [[ "${do_deploy}" == "true" ]]; then
			run_deploy
		else
			echo "Pinned ${values_file}; skipped deploy (--no-deploy)."
		fi
		printf 'Hot release complete at revision %s\n' "${revision}"
		exit 0
		;;

	full|fast)
		[[ -n "${version}" ]] || { echo "--version is required for mode ${mode}" >&2; exit 2; }
		[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || {
			echo "--version must be a v-prefixed SemVer value" >&2
			exit 2
		}

		if [[ "${mode}" == "fast" ]]; then
			# Kind e2e is the slow gate; keep unit/verify unless explicitly skipped.
			:
		fi

		if [[ "${do_tag}" == "true" ]]; then
			# Create/verify annotated tag at HEAD and push so registries/peers share it.
			"${repo_root}/hack/ensure-release-tag.sh" --version "${version}" --push
		else
			# publish-images still requires a matching local tag at HEAD.
			"${repo_root}/hack/ensure-release-tag.sh" --version "${version}"
		fi

		publish_args=(
			--prefix "${prefix}"
			--version "${version}"
			--platform "${platform}"
			--output "${output_dir}"
		)
		if [[ -n "${chart_registry}" ]]; then
			publish_args+=(--chart-registry "${chart_registry}")
		fi
		if [[ "${mode}" == "fast" || "${skip_verification}" == "true" ]]; then
			# fast always skips kind-e2e path inside publish-release via --skip-verification,
			# but still runs make verify once here for safety unless fully skipped.
			if [[ "${skip_verification}" != "true" ]]; then
				make -C "${repo_root}" verify
			fi
			publish_args+=(--skip-verification)
		elif [[ "${skip_verification}" == "true" ]]; then
			publish_args+=(--skip-verification)
		fi

		# Primaris release security gate (mirrors .hazyforge/tests.yaml suite security):
		# 1) source: govulncheck + gosec
		# 2) containers: Trivy HIGH/CRITICAL on all seven images
		# Evidence: dist/security/trivy/summary.txt and per-image reports.
		if [[ "${skip_verification}" != "true" ]]; then
			echo "Running Primaris release security gate (make security-release)..."
			echo "  - source scans (govulncheck, gosec)"
			echo "  - Trivy scan for each container (controller + six runners)"
			make -C "${repo_root}" security-release \
				IMAGE_TAG="security-${version}" \
				RELEASE_PLATFORM="${platform}" \
				IMAGE_PREFIX=""
			if [[ ! -f "${repo_root}/dist/security/trivy/summary.txt" ]]; then
				echo "missing Trivy summary evidence at dist/security/trivy/summary.txt" >&2
				exit 1
			fi
			grep -q 'RESULT=PASS' "${repo_root}/dist/security/trivy/summary.txt" || {
				echo "Trivy summary did not report RESULT=PASS" >&2
				cat "${repo_root}/dist/security/trivy/summary.txt" >&2
				exit 1
			}
			echo "Primaris release security evidence:"
			cat "${repo_root}/dist/security/trivy/summary.txt"
		fi

		"${repo_root}/hack/publish-release.sh" "${publish_args[@]}"

		lock="${output_dir}/images-${version}.lock.tsv"
		pin_from_lock "${lock}"

		if [[ "${do_github}" == "true" ]]; then
			gh_args=(
				--repo "${RELEASE_REPO:-HazyForge/anvil-agents}"
				--version "${version}"
				--output "${output_dir}"
				--chart-registry "${chart_registry:-oci://${prefix}/charts}"
			)
			"${repo_root}/hack/create-github-release.sh" "${gh_args[@]}"
		fi

		if [[ "${do_deploy}" == "true" ]]; then
			deploy_extra+=(--manifests)
			run_deploy
		else
			echo "Pinned Primaris overlay at ${values_file}"
			echo "Commit/push for Argo, or run: ./hack/deploy-primaris.sh --manifests"
		fi

		printf 'Primaris release %s complete (mode=%s)\n' "${version}" "${mode}"
		exit 0
		;;
esac
