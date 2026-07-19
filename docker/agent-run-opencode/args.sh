#!/usr/bin/env bash

# Return success only for presentation and logging arguments that cannot
# replace controller-owned non-interactive execution, permission, workspace,
# output, session, prompt, provider, or model settings. Options with values use
# a single --option=value item so a positional prompt cannot be smuggled in as
# the following JSON array item.
anvil_opencode_additional_arg_allowed() {
	case "${1:-}" in
		--print-logs|--thinking|--log-level=DEBUG|--log-level=INFO|--log-level=WARN|--log-level=ERROR|--title=?*)
			return 0
			;;
	esac
	return 1
}
