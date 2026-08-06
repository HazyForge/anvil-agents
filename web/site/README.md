# Anvil Agents product site

Public marketing site for [Anvil Agents](https://github.com/HazyForge/anvil-agents),
served at **https://anvil-agents.hazyforge.io**.

## Direction

- Product face of the open-source operator (not the OIDC console).
- Visual language: hunter-green Anvil surfaces (ink/ice/emerald), sharper than
  generic SaaS, related to Hazy Forge without cloning the blue-forward landing.
- Hero: a full-bleed cinematic film generated with Grok Imagine (anvil forge +
  Kubernetes job grid, hunter-green on ink) playing behind the copy as a muted
  autoplay loop, with a static poster fallback for reduced-motion / data-saver
  users. No canvas 3D, no WebGL — the video is a single optimized MP4.

## Local development

```bash
cd web/site
pnpm install
pnpm dev
```

Docs are loaded live from the monorepo `docs/` tree (Vite alias `@repo-docs`).
Diagrams live in `docs/images/` and are synced into `public/docs/` on
`pnpm dev` / `pnpm build`.

```bash
pnpm build
pnpm preview
# open http://127.0.0.1:4173/docs
```

## Container

Build from the **repository root** so operator markdown is embedded:

```bash
docker build -f web/site/Dockerfile -t ghcr.io/hazyforge/anvil-agents-site:dev .
docker run --rm -p 8080:8080 ghcr.io/hazyforge/anvil-agents-site:dev
```

Probes: `GET /healthz`, `GET /readyz`.
Routes: `/`, `/docs`, `/docs/<slug>` (SPA fallback via nginx).

## Production wiring

- Image: `ghcr.io/hazyforge/anvil-agents-site`
- Cluster: hazy-sites
- Hostname: `anvil-agents.hazyforge.io`
- Manifests live under Anvil Primaris
  `clusters/prod/hazy-sites/namespaces/anvil-agents-site/`
  plus the gateway cert/listener for that hostname.
