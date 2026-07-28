# anvil-agents

This repository is the standalone owner of the Hazy Forge agent execution
operator, its CRDs, controller image, runner images, samples, and runtime
documentation.

## Ownership

- Preserve `control.anvil.hazyforge.io/v1alpha1` until an explicitly planned API
  migration. Existing AgentDataVolume PVC owner references depend on it.
- AgentRun is append-only. New execution intent creates a new AgentRun.
- Application and ApplicationTarget references are opaque scope metadata. Do
  not import or read anvil-primaris API types from this repository.
- Manager authorization, repository mutation, and product delivery policy are
  external policy-plane concerns. Do not add imports from anvil-primaris or
  Anvil Hub to implement them here.
- Avoid running this controller and the former anvil-primaris agent
  reconcilers at the same time.
- Keep the optional OIDC-facing AgentRun API in a separate process and
  ServiceAccount from the controller. It may read AgentRuns and their verified
  Job, Pod, and `agent` container logs. When `runs.createEnabled=true` it may
  create append-only AgentRuns (never update existing runs). Opt-in composition
  library endpoints may get/list (and, when `composition.writeEnabled=true`,
  create/update/delete) `AgentRunProfile`, `AgentHarnessProfile`,
  `AgentSkillSet`, `AgentToolSet`, `VolumeProfile`, and `AgentDataVolume`
  objects. Writes are denied for GitOps-owned objects and for any object that is
  not labeled `control.anvil.hazyforge.io/managed-by=anvil-agents-console` so
  Git remains source of truth for fleet config. The console presents
  `AgentRunProfile` objects as composition cards; other composition kinds live
  under Library. The API must not acquire Secret access or policy-broker
  authority.
- OIDC configuration must remain provider-neutral and deny by default. Require
  an exact issuer, audience, explicit claim binding, namespace authorization,
  and exact CORS origins. Never accept access tokens in query strings or allow
  clients to choose arbitrary Pods, Jobs, or containers.

## Validation

Run `make verify` before publishing. Security is an **independent public
GitHub Actions** program (`.github/workflows/security.yml`): govulncheck,
gosec, CodeQL, owned-deps (`anvil-hotline`), Trivy filesystem, and Trivy on
every container. Primaris only installs this operator via
`.hazyforge/clusters/anvil-primaris/` — it does not own security gates. See
`docs/security-and-release.md`. Regenerate API objects, CRDs, and chart CRDs
with `make manifests` whenever API markers change. Use `hack/build-images.sh`
for local checks, builds, and registry pushes so local, CI, and release
workflows keep one image/component mapping. Run
`bash -n hack/stream-agent-run.sh` after changing the live-stream helper, and
render the chart with both `api.enabled=false` and a complete enabled API
configuration.

## Local release to Anvil Primaris

GitHub Actions is never required. Prefer local Docker Buildx:

- `make release-primaris-hot` — controller-only build/push, pin Primaris
  `deploy.yaml`, helm-deploy chart+CRDs (fast console/API loops).
- `VERSION=vX.Y.Z make release-primaris-fast` — seven-image publish without Kind
  e2e, pin Primaris digests (add `RELEASE_DEPLOY=true` for live apply).
- `VERSION=vX.Y.Z make release-primaris` — full gates including Kind e2e.
- `make deploy-primaris` — apply local chart + current Primaris overlay only.

Details: `docs/release-primaris.md`.
