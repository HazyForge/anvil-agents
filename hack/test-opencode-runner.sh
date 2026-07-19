#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../docker/agent-run-opencode/args.sh
source "${repo_root}/docker/agent-run-opencode/args.sh"

for arg in \
	--attach --attach=http://example.invalid \
	--interactive --interactive=true -i -i=true \
	--mini --mini=true \
	--command --command=review \
	--share --share=true \
	--auto --auto=true \
	--yolo --yolo=true \
	--dangerously-skip-permissions --dangerously-skip-permissions=true \
	--dir --dir=/tmp \
	--continue --continue=true -c -c=true \
	--session --session=abc -s -s=abc \
	--fork --fork=true \
	--format --format=default \
	--pure --pure=true --no-pure \
	--model --model=openai/example -m -m=openai/example \
	--agent --agent=review \
	--variant --variant=high \
	--file --file=/etc/passwd -f -f=/etc/passwd \
	--port --port=4096 \
	--password --password=secret -p -p=secret \
	--username --username=operator -u -u=operator \
	--title --title= \
	review --unknown --log-level=TRACE; do
	if anvil_opencode_additional_arg_allowed "${arg}"; then
		echo "forbidden OpenCode additional argument was accepted: ${arg}" >&2
		exit 1
	fi
done

for arg in --title=review --thinking --print-logs --log-level=INFO; do
	if ! anvil_opencode_additional_arg_allowed "${arg}"; then
		echo "safe OpenCode additional argument was rejected: ${arg}" >&2
		exit 1
	fi
done

echo "OpenCode runner argument contract passed"
