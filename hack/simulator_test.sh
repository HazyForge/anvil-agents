#!/usr/bin/env bash
# Offline checks for the local simulator: every scenario plays to its intended
# outcome in every persona, and every scenario a seed file names exists.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
harness_dir="${root_dir}/examples/simulator/harness"
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

python3 -m py_compile "${harness_dir}/sim_agent.py"

# scenario -> "expected exit code:expected final report type"
declare -A expected=(
	[docs-refresh]="0:decision"
	[failing-build]="1:decision"
	[flaky-test-hunt]="0:decision"
	[incident-triage]="0:decision"
	[long-migration]="0:decision"
	[pr-review]="0:decision"
	[release-dry-run]="0:decision"
	[security-sweep]="0:needsHuman"
)

for path in "${harness_dir}"/scenarios/*.json; do
	scenario="$(basename "${path}" .json)"
	[[ -n "${expected[${scenario}]:-}" ]] || {
		echo "scenario ${scenario} has no expectation in hack/simulator_test.sh" >&2
		exit 1
	}
	want_exit="${expected[${scenario}]%%:*}"
	want_type="${expected[${scenario}]#*:}"
	printf 'Task.\nsim-scenario: %s\n' "${scenario}" >"${work_dir}/prompt"
	echo '{}' >"${work_dir}/context.json"
	for persona in codex hermes opencode pi; do
		set +e
		ANVIL_AGENT_RUN_PROMPT_FILE="${work_dir}/prompt" \
			ANVIL_AGENT_RUN_CONTEXT_FILE="${work_dir}/context.json" \
			ANVIL_AGENT_RUN_STATUS_FILE="${work_dir}/status-${scenario}-${persona}.jsonl" \
			SIM_SCENARIO_DIR="${harness_dir}/scenarios" SIM_SPEED=0 SIM_PERSONA="${persona}" \
			python3 "${harness_dir}/sim_agent.py" >"${work_dir}/out" 2>&1
		got_exit=$?
		set -e
		[[ "${got_exit}" == "${want_exit}" ]] || {
			echo "${scenario}/${persona}: exit ${got_exit}, want ${want_exit}" >&2
			cat "${work_dir}/out" >&2
			exit 1
		}
		got_type="$(tail -n 1 "${work_dir}/status-${scenario}-${persona}.jsonl" | python3 -c 'import json,sys; print(json.load(sys.stdin)["type"])')"
		[[ "${got_type}" == "${want_type}" ]] || {
			echo "${scenario}/${persona}: final report ${got_type}, want ${want_type}" >&2
			exit 1
		}
		grep -q "^ANVIL_AGENT_RUN_STATUS_JSON={\"type\":\"${want_type}\"" "${work_dir}/out" || {
			echo "${scenario}/${persona}: status line missing from logs" >&2
			exit 1
		}
	done
done

# Every scenario named in a seed file must exist.
while IFS= read -r scenario; do
	[[ -f "${harness_dir}/scenarios/${scenario}.json" ]] || {
		echo "seed references unknown scenario ${scenario}" >&2
		exit 1
	}
done < <(grep -ho 'sim-scenario: [a-z0-9-]*' "${root_dir}"/examples/simulator/seed/*.yaml | awk '{print $2}' | sort -u)

# Every application a seed file names must be in the launch-controls binding.
applications_line="$(grep '^applications=(' "${root_dir}/hack/simulator.sh")"
while IFS= read -r application; do
	[[ " ${applications_line} " == *[\(\ ]"${application}"[\)\ ]* ]] || {
		echo "seed application ${application} is missing from applications in hack/simulator.sh" >&2
		exit 1
	}
done < <(grep -h -A1 'applicationRef:' "${root_dir}"/examples/simulator/seed/*.yaml | awk '$1 == "name:" {print $2}' | sort -u)

bash -n "${root_dir}/hack/simulator.sh"
echo "simulator checks passed"
