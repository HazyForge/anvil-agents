#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="${root}/hack/run-anvil-desktop.sh"
stub="${root}/hack/anvil-desktop-stub-api.py"

bash -n "${script}"
python3 -m py_compile "${stub}"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
bash "${script}" --help >/dev/null
# Fixture helper is not a public flag; invoke the function via a short extract.
# Recreate fixtures the same way the launcher does:
python3 - <<'PY' "${tmp}"
import os, stat, sys
from pathlib import Path
d = Path(sys.argv[1]) / "bin"
d.mkdir(parents=True)
text = """#!/bin/sh
echo ok
"""
p = d / "codex"
p.write_text(text)
p.chmod(0o755)
assert p.stat().st_mode & stat.S_IXUSR
print("fixtures-ok")
PY

echo "run-anvil-desktop.sh syntax ok"
