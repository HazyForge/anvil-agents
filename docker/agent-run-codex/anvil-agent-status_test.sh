#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
status_tool="${root_dir}/docker/agent-run-codex/anvil-agent-status"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

export ANVIL_AGENT_RUN_STATUS_FILE="${test_dir}/status.jsonl"
export ANVIL_AGENT_RUN_STATUS_LOG_FD=""
export ANVIL_AGENT_RUN_EFFECT_ACTOR="agent:manager"
export ANVIL_AGENT_RUN_EFFECT_EXECUTOR="anvilctl"

assert_status_line() {
	local line_number="$1"
	local expression="$2"
	jq -e -s ".[${line_number}] | ${expression}" "${ANVIL_AGENT_RUN_STATUS_FILE}" >/dev/null
}

receipt_args=(
	--operation-id push-master-001
	--effect-kind git.ref.update
	--target github:HazyForge/anvil-primaris:refs/heads/master
	--intent-digest sha256:intent
	--idempotency-key run-001:push-master-001
)

"${status_tool}" effect-started "${receipt_args[@]}" --summary "Pushing the accepted commit." >/dev/null
assert_status_line 0 '
	.type == "effect" and
	.effect.operationID == "push-master-001" and
	.effect.kind == "git.ref.update" and
	.effect.state == "Started" and
	.effect.target == "github:HazyForge/anvil-primaris:refs/heads/master" and
	.effect.intentDigest == "sha256:intent" and
	.effect.idempotencyKey == "run-001:push-master-001" and
	.effect.actor == "agent:manager" and
	.effect.executor == "anvilctl" and
	.effect.message == "Pushing the accepted commit." and
	(.effect.startedAt | test("Z$")) and
	(.effect | has("completedAt") | not)'

"${status_tool}" effect-confirmed "${receipt_args[@]}" \
	--external-ref f7a6f57b \
	--external-url https://github.com/HazyForge/anvil-primaris/commit/f7a6f57b \
	--summary "Remote ref readback confirmed the commit." >/dev/null
assert_status_line 1 '
	.type == "effect" and
	.effect.state == "Confirmed" and
	.effect.externalRef == "f7a6f57b" and
	.effect.externalURL == "https://github.com/HazyForge/anvil-primaris/commit/f7a6f57b" and
	(.effect.completedAt | test("Z$")) and
	(.effect.verifiedAt | test("Z$")) and
	(.effect | has("startedAt") | not)'

failed_args=(
	--operation-id release-001
	--effect-kind release.publish
	--target registry:ghcr.io/hazyforge/anvil-agents
	--intent-digest sha256:release-intent
	--idempotency-key run-001:release-001
)
"${status_tool}" effect-failed "${failed_args[@]}" --detail "Provider rejected the request before mutation." >/dev/null
assert_status_line 2 '
	.type == "effect" and
	.effect.state == "Failed" and
	.effect.message == "Provider rejected the request before mutation." and
	(.effect.completedAt | test("Z$")) and
	(.effect | has("verifiedAt") | not)'

"${status_tool}" effect-summary \
	--completeness Incomplete \
	--summary "One effect is confirmed; later effects may be unreported." >/dev/null
assert_status_line 3 '
	.type == "effectSummary" and
	.effectSummary.completeness == "Incomplete" and
	.effectSummary.summary == "One effect is confirmed; later effects may be unreported." and
	(.effectSummary | has("outcome") | not) and
	(.effectSummary | has("reconciliationRequired") | not)'

"${status_tool}" effect-summary \
	--completeness Complete \
	--summary "All declared effects are confirmed." >/dev/null
assert_status_line 4 '
	.effectSummary.completeness == "Complete" and
	(.effectSummary | has("outcome") | not) and
	(.effectSummary | has("reconciliationRequired") | not)'

"${status_tool}" progress --stage verify --summary "Existing reports remain compatible." >/dev/null
assert_status_line 5 '
	.type == "progress" and
	.stage == "verify" and
	.summary == "Existing reports remain compatible." and
	(. | has("effect") | not) and
	(. | has("effectSummary") | not)'

if "${status_tool}" effect-confirmed "${receipt_args[@]}" >/dev/null 2>"${test_dir}/missing-ref.err"; then
	echo "effect-confirmed accepted a receipt without --external-ref" >&2
	exit 1
fi
grep -q "external-ref is required" "${test_dir}/missing-ref.err"

if "${status_tool}" effect-summary --completeness Declared >/dev/null 2>"${test_dir}/invalid-completeness.err"; then
	echo "effect-summary accepted an unknown completeness value" >&2
	exit 1
fi
grep -q "Complete, Incomplete, or Unknown" "${test_dir}/invalid-completeness.err"

if "${status_tool}" --type effect "${receipt_args[@]}" >/dev/null 2>"${test_dir}/missing-state.err"; then
	echo "raw effect report accepted a missing state" >&2
	exit 1
fi
grep -q "state is selected" "${test_dir}/missing-state.err"

"${status_tool}" --type effect-started "${receipt_args[@]}" --summary "Flag-selected type." >/dev/null
assert_status_line 6 '
	.type == "effect" and
	.effect.state == "Started" and
	.effect.message == "Flag-selected type."'

printf 'anvil-agent-status effect receipt tests passed\n'
