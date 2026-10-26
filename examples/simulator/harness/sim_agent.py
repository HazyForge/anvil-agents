#!/usr/bin/env python3
"""Simulated Anvil Agents harness: a credential-free stand-in for a real agent.

It honours the custom harness contract (docs/harnesses.md): it reads the
mounted prompt and context, prints work as it goes, emits structured status
reports with the status log prefix, and exits nonzero when its scenario
fails. Nothing here calls a model, a repository or a cluster; every command
and output it prints is scripted. Use it to see what working agents look like
in the console and to exercise scheduling, controls and failure handling.

Scenario selection, first match wins:
  1. a line "sim-scenario: <name>" in the prompt
  2. SIM_SCENARIO in the environment (set on a harness profile)
  3. a stable pick from the run name

Other environment:
  SIM_PERSONA  codex | hermes | opencode | pi   (log style, default codex)
  SIM_SPEED    multiplier for every pause, e.g. 0.1 for quick tests (default 1)
"""

from __future__ import annotations

import hashlib
import json
import os
import random
import re
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

SCENARIO_DIR = Path(os.environ.get("SIM_SCENARIO_DIR", "/opt/anvil-sim/scenarios"))
STATUS_PREFIX = os.environ.get("ANVIL_AGENT_RUN_STATUS_LOG_PREFIX", "ANVIL_AGENT_RUN_STATUS_JSON=")
STATUS_FILE = os.environ.get("ANVIL_AGENT_RUN_STATUS_FILE", "")
SPEED = max(0.0, float(os.environ.get("SIM_SPEED", "1") or "1"))
PERSONA = (os.environ.get("SIM_PERSONA") or "codex").strip().lower()

# Plausible background work used by "work" steps to fill a stretch of time.
WORK_POOLS: dict[str, list[tuple[str, list[str]]]] = {
    "go": [
        ("rg -n 'func (r \\*AgentRunReconciler)' internal/controller", ["internal/controller/agent_run_controller.go:412:func (r *AgentRunReconciler) Reconcile(", "internal/controller/agent_run_controller.go:1588:func (r *AgentRunReconciler) buildJob("]),
        ("go test ./internal/controller -run TestAgentRun -count=1", ["ok  \tgithub.com/hazyforge/anvil-agents/internal/controller\t14.212s"]),
        ("go vet ./...", []),
        ("sed -n '540,580p' internal/controller/agent_run_controller.go", ["\tif status.StartedAt == nil {", "\t\tstatus.StartedAt = &now", "\t}"]),
        ("git log --oneline -5 -- internal/runapi", ["7414c7c feat(console): redesign Anvil Agents Console as Forge Deck", "2faf8ab Send ATE 0.0.8 actor_template_namespace on CreateActor."]),
    ],
    "rust": [
        ("cargo check -p hazy-trade-pipeline", ["    Checking hazy-trade-pipeline v0.42.0", "    Finished `dev` profile [unoptimized + debuginfo] target(s) in 38.41s"]),
        ("cargo nextest run -p hazy-trade-risk --no-fail-fast", ["    Starting 184 tests across 6 binaries", "        PASS [   0.031s] hazy-trade-risk::limits::rejects_oversized_order", "     Summary [  11.902s] 184 tests run: 184 passed, 0 skipped"]),
        ("rg -n 'fn settle' crates/", ["crates/hazy-trade-ledger/src/settle.rs:88:pub fn settle(batch: &Batch) -> Result<Receipt> {"]),
        ("git diff --stat origin/master...HEAD", [" crates/hazy-trade-risk/src/limits.rs | 46 +++++++++----", " 1 file changed, 31 insertions(+), 15 deletions(-)"]),
    ],
    "ops": [
        ("kubectl get pods -n hazy-trade -o wide", ["NAME                         READY   STATUS    RESTARTS   AGE", "hazy-trade-api-7c9d8f-x2kq   1/1     Running   0          3h12m", "hazy-trade-worker-5b7c-9tqw  1/1     Running   2          3h12m"]),
        ("kubectl logs deploy/hazy-trade-worker --since=15m | rg -c 'retrying'", ["37"]),
        ("logcli query '{namespace=\"hazy-trade\"} |= \"timeout\"' --since=1h --limit=5", ["2026-10-26T16:41:07Z worker: upstream quote timeout after 2s (venue=coinbase)", "2026-10-26T16:43:52Z worker: upstream quote timeout after 2s (venue=coinbase)"]),
        ("argocd app get hazy-trade --refresh", ["Sync Status:   Synced to master (1f3c2a9)", "Health Status: Healthy"]),
    ],
    "docs": [
        ("rg -l 'anvilctl agent' docs/", ["docs/cli.md", "docs/operations.md"]),
        ("markdownlint docs/**/*.md", ["docs/operations.md:112 MD013/line-length Line length [Expected: 80; Actual: 96]"]),
        ("wc -w docs/getting-started.md", ["2148 docs/getting-started.md"]),
    ],
    "security": [
        ("trivy image --severity HIGH,CRITICAL ghcr.io/hazyforge/hazy-trade-api:1.4.2", ["Total: 1 (HIGH: 1, CRITICAL: 0)", "│ golang.org/x/net │ CVE-2026-31337 │ HIGH │ 0.31.0 │ 0.33.0 │"]),
        ("trivy config charts/hazy-trade", ["Tests: 52 (SUCCESSES: 51, FAILURES: 1)", "AVD-KSV-0014 (HIGH): Container 'worker' should set 'securityContext.readOnlyRootFilesystem' to true"]),
        ("gitleaks detect --no-banner --redact", ["INF no leaks found"]),
        ("grype sbom:./sbom.spdx.json --only-fixed", ["NAME              INSTALLED  FIXED-IN  TYPE       VULNERABILITY   SEVERITY", "golang.org/x/net  v0.31.0    0.33.0    go-module  CVE-2026-31337  High"]),
    ],
}


def now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")


def emit(line: str = "") -> None:
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def pause(seconds: float) -> None:
    if seconds > 0 and SPEED > 0:
        time.sleep(seconds * SPEED)


class Persona:
    """Formats the same work in the voice of a different harness."""

    def __init__(self, name: str) -> None:
        self.name = name if name in {"codex", "hermes", "opencode", "pi"} else "codex"

    def banner(self, run: str, scenario: str) -> None:
        if self.name == "codex":
            emit(f"[{now()}] OpenAI Codex (simulated) · workdir: /workspace · sandbox: read-only · run {run}")
        elif self.name == "hermes":
            emit(f"Hermes Agent (simulated) · session {run} · toolsets: terminal, file, web")
        elif self.name == "opencode":
            emit(f"opencode (simulated) · run {run} · model: simulated/reasoner")
        else:
            emit(f"pi (simulated) · {run}")
        emit(f"scenario: {scenario}")

    def say(self, text: str) -> None:
        if self.name == "codex":
            emit(f"[{now()}] codex")
            emit(text)
        elif self.name == "hermes":
            emit(f"⚕ {text}")
        elif self.name == "opencode":
            emit(text)
        else:
            emit(f"› {text}")

    def think(self, text: str) -> None:
        if self.name == "codex":
            emit(f"[{now()}] thinking")
            emit(f"**{text}**")
        elif self.name == "hermes":
            emit(f"  (thinking) {text}")
        else:
            emit(f"~ {text}")

    def tool(self, cmd: str, out: list[str], ok: bool, ms: int) -> None:
        if self.name == "codex":
            emit(f"[{now()}] exec bash -lc {json.dumps(cmd)} in /workspace")
            emit(f"[{now()}] bash -lc {json.dumps(cmd)} {'succeeded' if ok else 'exited 1'} in {ms}ms:")
            for line in out:
                emit(line)
        elif self.name == "hermes":
            emit(f"  ┊ terminal  $ {cmd}  {ms / 1000:.1f}s{'' if ok else '  [exit 1]'}")
            for line in out:
                emit(f"  ┊   {line}")
        elif self.name == "opencode":
            emit(f"| Bash     {cmd}")
            for line in out:
                emit(f"|   {line}")
            if not ok:
                emit("|   exit status 1")
        else:
            emit(f"→ bash {cmd}")
            for line in out:
                emit(f"  {line}")
            if not ok:
                emit("  ✗ exit 1")

    def edit(self, path: str, added: int, removed: int) -> None:
        if self.name == "codex":
            emit(f"[{now()}] apply_patch auto_approved=true:")
            emit(f"M {path}")
            emit(f"[{now()}] apply_patch(auto_approved=true) exited 0 in 41ms:")
            emit(f"Success. Updated the following files:\nM {path}")
        elif self.name == "hermes":
            emit(f"  ┊ patch     {path}  +{added} -{removed}")
        elif self.name == "opencode":
            emit(f"| Edit     {path} (+{added} -{removed})")
        else:
            emit(f"→ edit {path} (+{added} -{removed})")

    def tokens(self, used: int) -> None:
        if self.name == "codex":
            emit(f"[{now()}] tokens used: {used:,}")
        elif self.name == "hermes":
            emit(f"  ⏱ tokens {used:,}")


def report(kind: str, **fields: object) -> None:
    payload = {"type": kind, "observedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")}
    payload.update({k: v for k, v in fields.items() if v not in (None, "")})
    body = json.dumps(payload, separators=(",", ":"))
    emit(f"{STATUS_PREFIX}{body}")
    if STATUS_FILE:
        try:
            Path(STATUS_FILE).parent.mkdir(parents=True, exist_ok=True)
            with open(STATUS_FILE, "a", encoding="utf-8") as handle:
                handle.write(body + "\n")
        except OSError:
            pass


def read_contract() -> tuple[str, dict]:
    prompt_path = os.environ.get("ANVIL_AGENT_RUN_PROMPT_FILE", "")
    context_path = os.environ.get("ANVIL_AGENT_RUN_CONTEXT_FILE", "")
    prompt = Path(prompt_path).read_text(encoding="utf-8") if prompt_path and Path(prompt_path).is_file() else ""
    context: dict = {}
    if context_path and Path(context_path).is_file():
        try:
            context = json.loads(Path(context_path).read_text(encoding="utf-8"))
        except (OSError, ValueError):
            context = {}
    return prompt, context


def run_name(context: dict) -> str:
    for key in ("agentRun", "run", "metadata"):
        value = context.get(key)
        if isinstance(value, dict) and value.get("name"):
            return str(value["name"])
    return os.environ.get("ANVIL_AGENT_RUN_NAME") or os.environ.get("HOSTNAME", "sim-run")


def choose_scenario(prompt: str, name: str) -> str:
    match = re.search(r"sim-scenario:\s*([a-z0-9-]+)", prompt)
    if match:
        return match.group(1)
    if os.environ.get("SIM_SCENARIO"):
        return os.environ["SIM_SCENARIO"].strip()
    names = sorted(p.stem for p in SCENARIO_DIR.glob("*.json"))
    if not names:
        return "none"
    digest = int(hashlib.sha256(name.encode()).hexdigest(), 16)
    return names[digest % len(names)]


def play(persona: Persona, steps: list[dict], rng: random.Random) -> int:
    tokens = 0
    for step in steps:
        pause(float(step.get("t", 0)))
        if "say" in step:
            persona.say(step["say"])
            tokens += rng.randint(400, 1800)
        elif "think" in step:
            persona.think(step["think"])
        elif "cmd" in step:
            persona.tool(step["cmd"], list(step.get("out", [])), bool(step.get("ok", True)), int(step.get("ms", rng.randint(80, 2400))))
        elif "edit" in step:
            persona.edit(step["edit"], int(step.get("added", rng.randint(3, 40))), int(step.get("removed", rng.randint(0, 20))))
        elif "report" in step:
            fields = dict(step["report"])
            if fields.get("type") == "decision":
                persona.tokens(tokens)
            report(fields.pop("type", "progress"), **fields)
        elif "work" in step:
            spec = step["work"]
            pool = WORK_POOLS.get(spec.get("pool", "go"), WORK_POOLS["go"])
            until = time.monotonic() + float(spec.get("seconds", 60)) * SPEED
            while time.monotonic() < until:
                cmd, out = rng.choice(pool)
                persona.tool(cmd, out, True, rng.randint(60, 9000))
                tokens += rng.randint(200, 900)
                pause(rng.uniform(4, 11))
        elif "log" in step:
            emit(step["log"])
        elif "exit" in step:
            return int(step["exit"])
    return 0


def main() -> int:
    prompt, context = read_contract()
    name = run_name(context)
    scenario = choose_scenario(prompt, name)
    persona = Persona(PERSONA)
    persona.banner(name, scenario)
    path = SCENARIO_DIR / f"{scenario}.json"
    if not path.is_file():
        emit(f"unknown simulator scenario {scenario!r}; known: {', '.join(sorted(p.stem for p in SCENARIO_DIR.glob('*.json')))}")
        report("decision", action="stop", summary=f"Unknown simulator scenario {scenario}.", residualRisk="Nothing was simulated.")
        return 2
    script = json.loads(path.read_text(encoding="utf-8"))
    emit(f"read prompt ({len(prompt)} bytes) and context ({len(json.dumps(context))} bytes)")
    rng = random.Random(f"{name}/{scenario}")
    return play(persona, script.get("steps", []), rng)


if __name__ == "__main__":
    sys.exit(main())
