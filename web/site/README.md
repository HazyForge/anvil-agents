# Anvil Agents product site

Public marketing site for [Anvil Agents](https://github.com/HazyForge/anvil-agents),
served at **https://anvil-agents.hazyforge.io**.

## Direction

- Product face of the open-source operator (not the OIDC console).
- Visual language: hunter-green Anvil surfaces (ink/ice/emerald), sharper than
  generic SaaS, related to Hazy Forge without cloning the blue-forward landing.
- Hero “video”: interactive canvas cluster field evolved from the
  `hazyforge.io` forge wave background — perspective job-capacity contours and
  traveling job sparks, with an improved multi-harness Agent Council panel.

## Local development

```bash
cd web/site
pnpm install
pnpm dev
```

```bash
pnpm build
pnpm preview
```

## Container

```bash
docker build -t ghcr.io/hazyforge/anvil-agents-site:dev web/site
docker run --rm -p 8080:8080 ghcr.io/hazyforge/anvil-agents-site:dev
```

Probes: `GET /healthz`, `GET /readyz`.

## Production wiring

- Image: `ghcr.io/hazyforge/anvil-agents-site`
- Cluster: hazy-sites
- Hostname: `anvil-agents.hazyforge.io`
- Manifests live under Anvil Primaris
  `clusters/prod/hazy-sites/namespaces/anvil-agents-site/`
  plus the gateway cert/listener for that hostname.
