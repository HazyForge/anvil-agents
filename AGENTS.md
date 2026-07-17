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
- Keep the optional OIDC-facing AgentRun API in a separate process,
  ServiceAccount, and read-only RBAC boundary from the controller. It may read
  AgentRuns and their verified Job, Pod, and `agent` container logs; it must not
  acquire Secret access, mutation verbs, or policy-broker authority.
- OIDC configuration must remain provider-neutral and deny by default. Require
  an exact issuer, audience, explicit claim binding, namespace authorization,
  and exact CORS origins. Never accept access tokens in query strings or allow
  clients to choose arbitrary Pods, Jobs, or containers.

## Validation

Run `make verify` before publishing. Regenerate API objects, CRDs, and chart CRDs
with `make manifests` whenever API markers change. Use
`hack/build-images.sh` for local checks, builds, and registry pushes so local,
CI, and release workflows keep one image/component mapping. Run
`bash -n hack/stream-agent-run.sh` after changing the live-stream helper, and
render the chart with both `api.enabled=false` and a complete enabled API
configuration.
