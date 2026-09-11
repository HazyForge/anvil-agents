# Anvil Agents faces

Simple eye faces for **Anvil Agents** agents — Grokbot-like, vector, idle-blink.
This folder is the shared asset pack. Anvil Agents Console consumes it today.
Anvil Agents Desktop ([PR 174](https://github.com/HazyForge/anvil-agents/pull/174))
and later iOS/Android clients should copy or bundle these files. Do not put
avatars under `cmd/desktop` or `web/desktop`.

Not Anvil Primaris.

## Files

| Path | Role |
| --- | --- |
| `scene.json` | Scene graph: 12 faces, geometry, legacy JPEG ids |
| `animation.json` | Blink / glance clips + idle schedule (the porting hook) |
| `render.js` | `scene.json` → SVG string (no DOM, no CSS) |
| `player.js` | Idle player: SVG `transform` attributes only |
| `faces/*.svg` | Generated static SVGs (`node generate.mjs`) |
| `manifest.json` | Generated id / path / legacy index |
| `preview.html` | Serve this folder over HTTP to see blink |

Canonical stored annotation value is the face **id** (`herald`, `forge`, …)
on `ui.anvil.hazyforge.io/icon`. That id is portable to phones. Console-only
paths like `/avatars/robot-01.jpg` still resolve through `legacy`.

## Scene graph

Each SVG (and each `render.js` output) has:

- `svg[data-anvil-avatar="{id}"]`
- `#face` / `#face-disk`
- `#eyes`
- `#eye-left` and `#eye-right` (`data-part="eye"`), optional `#eye-center`
- optional `#pupil-left` / `#pupil-right` (`data-part="pupil"`)

Every eye group is pre-centered:

```
data-base-transform="translate(cx cy)[ rotate(deg)]"
transform="…same…"
```

Eye artwork sits at local `(0,0)`. Blink is `scale(1, sy)` composed **after**
that base transform. Glance is `translate(x, 0)` on pupil groups.

No CSS `@keyframes`. No SVG filters. No raster. That is the native contract.

## Animation hook

`animation.json` `clips.blink` and `clips.glance` are linear keyframes
`t ∈ [0,1]` → property value.

Web: `attachIdle(svgElement, animationSpec, { seed })`.

Swift later: `CGAffineTransform` scaleY on the eye layers; skip when
Reduce Motion is on.

Android later: `ObjectAnimator` on `scaleY`, pivot at the group origin; skip
when animator duration scale is 0.

Honor `prefers-reduced-motion` / the platform equivalent. Static open eyes
are the reduced-motion pose.

## Console

Vite copies this folder to `/avatars/` in the console build. The SPA inlines
SVG via `render.js` so blink runs (SMIL/`<img>` would not). `cmd/desktop` is
untouched.

```bash
node assets/agent-avatars/generate.mjs
node --check assets/agent-avatars/render.js
python3 -m http.server -d assets/agent-avatars 4173
# open http://127.0.0.1:4173/preview.html
```
