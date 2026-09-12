#!/usr/bin/env python3
"""Call DeepSeek with the mounted AgentRun prompt and emit council JSON."""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request

STATUS_PREFIX = os.environ.get("ANVIL_AGENT_RUN_STATUS_LOG_PREFIX", "ANVIL_AGENT_RUN_STATUS_JSON=")
DECISION_PREFIX = "ANVIL_COUNCIL_DECISION="


def main() -> int:
    prompt_path = os.environ.get("ANVIL_AGENT_RUN_PROMPT_FILE")
    if not prompt_path:
        print("missing ANVIL_AGENT_RUN_PROMPT_FILE", file=sys.stderr)
        return 2
    try:
        with open(prompt_path, encoding="utf-8") as handle:
            prompt = handle.read().strip()
    except OSError as err:
        print(f"read prompt: {err}", file=sys.stderr)
        return 2
    if not prompt:
        print("prompt file is empty", file=sys.stderr)
        return 2

    api_key = os.environ.get("DEEPSEEK_API_KEY", "").strip()
    if not api_key:
        print("DEEPSEEK_API_KEY is not set", file=sys.stderr)
        return 2

    base = os.environ.get("DEEPSEEK_BASE_URL", "https://api.deepseek.com").rstrip("/")
    model = os.environ.get("DEEPSEEK_MODEL", "deepseek-chat")
    body = {
        "model": model,
        "temperature": 0.7,
        "response_format": {"type": "json_object"},
        "messages": [
            {
                "role": "system",
                "content": (
                    "You are a council conversation harness. Return only the JSON "
                    "object described in the user prompt. Decide from the operator "
                    "message; do not emit a fixed script."
                ),
            },
            {"role": "user", "content": prompt},
        ],
    }
    request = urllib.request.Request(
        f"{base}/chat/completions",
        data=json.dumps(body).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {api_key}",
        },
        method="POST",
    )
    print(
        f"{STATUS_PREFIX}{json.dumps({'type': 'progress', 'stage': 'llm', 'summary': 'Calling the council model.'})}",
        flush=True,
    )
    try:
        with urllib.request.urlopen(request, timeout=90) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        print(f"llm http {err.code}: {detail[:400]}", file=sys.stderr)
        return 1
    except urllib.error.URLError as err:
        print(f"llm request failed: {err}", file=sys.stderr)
        return 1

    try:
        content = payload["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError) as err:
        print(f"unexpected llm payload: {err}", file=sys.stderr)
        return 1
    if not isinstance(content, str) or not content.strip():
        print("llm returned an empty message", file=sys.stderr)
        return 1

    decision = extract_json_object(content)
    if decision is None:
        print("llm message was not JSON", file=sys.stderr)
        print(content[:800], file=sys.stderr)
        return 1
    if not isinstance(decision, dict) or not (decision.get("anvil") or decision.get("reply") or decision.get("utterances")):
        print("llm JSON is not a council decision", file=sys.stderr)
        return 1

    encoded = json.dumps(decision, separators=(",", ":"))
    print(content.strip())
    print(f"{DECISION_PREFIX}{encoded}", flush=True)
    print(
        f"{STATUS_PREFIX}{json.dumps({'type': 'decision', 'action': 'confer', 'summary': 'Council harness returned a decision.'})}",
        flush=True,
    )
    return 0


def extract_json_object(raw: str):
    text = raw.strip()
    if text.startswith("```"):
        text = text.strip("`")
        if text.startswith("json"):
            text = text[4:]
        text = text.strip()
    start = text.find("{")
    end = text.rfind("}")
    if start < 0 or end <= start:
        return None
    try:
        return json.loads(text[start : end + 1])
    except json.JSONDecodeError:
        return None


if __name__ == "__main__":
    sys.exit(main())
