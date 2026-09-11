#!/usr/bin/env bash
# Validate the shared Anvil Agents face pack (scene graph + generated SVG).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
pack="$root/assets/agent-avatars"

node --check "$pack/render.js"
node --check "$pack/player.js"
node --check "$pack/generate.mjs"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cp -a "$pack/." "$tmp/pack"
(cd "$tmp/pack" && node generate.mjs)

if ! diff -u "$pack/manifest.json" "$tmp/pack/manifest.json"; then
  echo "assets/agent-avatars/manifest.json is stale; run node assets/agent-avatars/generate.mjs" >&2
  exit 1
fi

node --input-type=module <<EOF
import scene from '$pack/scene.json' with { type: 'json' };
import { resolveFaceId } from '$pack/render.js';
const cases = [
  ['herald', 'herald'],
  ['anvil-avatar:forge', 'forge'],
  ['/avatars/robot-01.jpg', 'herald'],
  ['robot-12', 'forge'],
  ['/avatars/scout.svg', 'scout'],
  ['scout.svg', 'scout'],
  ['', ''],
  ['https://example.com/x.png', ''],
];
for (const [raw, want] of cases) {
  const got = resolveFaceId(scene, raw);
  if (got !== want) {
    throw new Error(\`resolveFaceId(\${JSON.stringify(raw)}) => \${JSON.stringify(got)}, want \${JSON.stringify(want)}\`);
  }
}
console.log('ok resolveFaceId');
EOF

python3 - "$pack" "$tmp/pack" <<'PY'
import json, sys
from pathlib import Path

pack = Path(sys.argv[1])
fresh = Path(sys.argv[2])
scene = json.loads((pack / "scene.json").read_text())
animation = json.loads((pack / "animation.json").read_text())
manifest = json.loads((pack / "manifest.json").read_text())

if scene.get("product") != "Anvil Agents":
    raise SystemExit("scene.json product must be Anvil Agents")
if animation.get("product") != "Anvil Agents":
    raise SystemExit("animation.json product must be Anvil Agents")

faces = scene["faces"]
if len(faces) < 8:
    raise SystemExit("expected a family of faces")
ids = [f["id"] for f in faces]
if len(ids) != len(set(ids)):
    raise SystemExit("duplicate face ids")
if "forge" not in ids or "companion" not in ids:
    raise SystemExit("forge and companion faces are required")

for face in faces:
    svg_path = pack / "faces" / f"{face['id']}.svg"
    generated = fresh / "faces" / f"{face['id']}.svg"
    if not svg_path.is_file():
        raise SystemExit(f"missing {svg_path}")
    if svg_path.read_text() != generated.read_text():
        raise SystemExit(f"{svg_path} is stale; run node assets/agent-avatars/generate.mjs")
    svg = svg_path.read_text()
    if 'data-anvil-avatar="%s"' % face["id"] not in svg:
        raise SystemExit(f"{face['id']} missing data-anvil-avatar")
    if 'id="eye-left"' not in svg or 'id="eye-right"' not in svg:
        raise SystemExit(f"{face['id']} must define eye-left and eye-right")
    if 'data-part="eye"' not in svg:
        raise SystemExit(f"{face['id']} missing data-part=eye")
    if "data-base-transform=" not in svg:
        raise SystemExit(f"{face['id']} missing data-base-transform")
    if "@keyframes" in svg or "<style" in svg:
        raise SystemExit(f"{face['id']} must not ship CSS; native clients cannot port it")
    if "<filter" in svg or "feGaussian" in svg:
        raise SystemExit(f"{face['id']} must not use SVG filters")
    if len(svg.encode()) > 8192:
        raise SystemExit(f"{face['id']} SVG is unexpectedly large")

if "blink" not in animation.get("clips", {}):
    raise SystemExit("animation.json must define clips.blink")
if animation["clips"]["blink"].get("property") != "scaleY":
    raise SystemExit("blink must be scaleY on eye groups")

jpg = list((pack.parent.parent / "web/console/public").glob("**/*.jpg"))
if jpg:
    raise SystemExit(f"console still ships raster avatars: {jpg}")

print(f"ok {len(faces)} Anvil Agents faces")
PY
