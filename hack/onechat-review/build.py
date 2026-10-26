#!/usr/bin/env python3
"""Build the One Chat Review page from onechat scenario traces.

    build.py --out review.html --model "Claude Sonnet" trace1.json trace2.json ...
"""
import argparse
import json
import os
import re
from datetime import datetime, timezone

here = os.path.dirname(os.path.abspath(__file__))

parser = argparse.ArgumentParser()
parser.add_argument("--out", required=True)
parser.add_argument("--model", default="Claude")
parser.add_argument("traces", nargs="+")
args = parser.parse_args()

scenarios = []
for path in args.traces:
    with open(path) as handle:
        trace = json.load(handle)
    # Local paths are useful but long; keep the last two components so the page stays readable.
    for turn in trace.get("turns") or []:
        for action in turn.get("actions") or []:
            local = (action.get("receipt") or {}).get("local")
            if local and local.get("workdir"):
                local["workdir"] = "…/" + "/".join(local["workdir"].rstrip("/").split("/")[-2:])
    scenarios.append(trace)

ran = min((s.get("startedAt") for s in scenarios if s.get("startedAt")), default=None)
data = {"scenarios": scenarios, "managerModel": args.model, "ranAt": ran or datetime.now(timezone.utc).isoformat()}
payload = json.dumps(data, ensure_ascii=False)
# Scratch and home paths from this workstation say nothing to a reader; keep the tail.
payload = re.sub(r"/tmp/claude-\d+/[^\"\s]*?/runs/[^/\"]+/", "…/", payload)
payload = re.sub(r"/tmp/claude-\d+/[^\s`\"\\]*", "…/scratch", payload)
payload = re.sub(r"/home/[a-z0-9_-]+/", "~/", payload)
payload = payload.replace("</", "<\\/")

with open(os.path.join(here, "template.html")) as handle:
    page = handle.read().replace("__TRACES__", payload)
with open(args.out, "w") as handle:
    handle.write(page)
print(args.out, f"{len(page) / 1024:.0f} KiB")
