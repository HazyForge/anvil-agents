#!/bin/sh
set -eu

prompt_file="${ANVIL_AGENT_RUN_PROMPT_FILE:?missing prompt file}"
context_file="${ANVIL_AGENT_RUN_CONTEXT_FILE:?missing context file}"
status_prefix="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"

test -s "${prompt_file}"
test -s "${context_file}"

printf 'demo harness read %s and %s\n' "${prompt_file}" "${context_file}"
printf '%s%s\n' "${status_prefix}" '{"type":"decision","classification":"smoke-test","action":"inspect","summary":"The custom harness contract completed successfully.","residualRisk":"No model provider was exercised."}'
