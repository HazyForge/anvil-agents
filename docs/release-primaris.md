# Local release to Anvil Primaris (no GitHub Actions)

Anvil Agents is published and deployed entirely with **local Docker Buildx**,
repository-owned scripts, and the first-party Primaris overlay under
`.hazyforge/clusters/anvil-primaris/`. GitHub Actions is optional convenience
only — never a release prerequisite.

The Primaris consumer is discovered by the Anvil Primaris `remote-helm`
ApplicationSet from:

```text
.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml
```

That file pins controller + six runner digests and sets `crds.install: true` so
Helm applies the full CRD set (AgentRun, composition kinds, AgentAuthSession,
…) with keep/prune-safe annotations.

## Prerequisites

- Docker with Buildx (logged into `ghcr.io` for push)
- Helm 3 and kubectl (for live deploy)
- Clean git worktree for versioned releases (`full` / `fast`)
- Cluster context that can reach Primaris (for `--deploy` / hot path)

```bash
docker login ghcr.io
helm registry login ghcr.io   # full/fast chart push only
kubectl config current-context
```

## Modes

| Mode | Command | What it does |
|------|---------|----------------|
| **full** | `VERSION=vX.Y.Z make release-primaris` | Tag+push, `make verify` + Kind e2e, build/push 7 images, OCI chart, pin Primaris digests |
| **fast** | `VERSION=vX.Y.Z make release-primaris-fast` | Same without Kind e2e (still `make verify`); good trusted cutovers |
| **hot** | `make release-primaris-hot` | Rebuild/push **controller** only, pin its digest, **helm deploy** chart+CRDs now |
| **deploy** | `make deploy-primaris` | Apply local chart + current `deploy.yaml` only (no build) |

Equivalent scripts:

```bash
./hack/release-primaris.sh --mode full  --version v0.1.14
./hack/release-primaris.sh --mode fast  --version v0.1.14 --deploy
./hack/release-primaris.sh --mode hot   --component controller --allow-dirty
./hack/release-primaris.sh --mode deploy --manifests
./hack/deploy-primaris.sh --manifests
```

### full / fast flags

```bash
# Live cluster apply after pin (bypasses waiting for Argo):
VERSION=v0.1.14 make release-primaris-fast RELEASE_DEPLOY=true

# Also publish GitHub Release page (optional):
VERSION=v0.1.14 make release-primaris RELEASE_GITHUB=true
```

After a versioned release without `--deploy`, commit the updated
`deploy.yaml` so Argo’s remote-helm app picks up digests on the next sync.

### hot path (console / API / controller)

```bash
# Dirty tree OK — pins only image.reference, deploys immediately:
make release-primaris-hot

# Specific component:
make release-primaris-hot COMPONENT=controller

# Pin only, no cluster write:
make release-primaris-hot RELEASE_DEPLOY=false
```

Hot mode does **not** rebuild runners. Use `fast`/`full` when runner images or
the seven-image lock must move together.

### deploy-only (CRDs + chart)

```bash
make deploy-primaris
# or
./hack/deploy-primaris.sh --manifests --context <kube-context>
```

Helm release name defaults to `anvil-agents-system-chart` (ApplicationSet
pattern `<namespace-dir>-chart`). `fullnameOverride` defaults to `anvil-agents`.
`crds.install: true` in the Primaris overlay applies CRDs from
`charts/anvil-agents/templates/crds.yaml` (regenerated via `make manifests`).

## End-to-end recipes

### A. Ship a console change to Primaris in one command

```bash
cd /path/to/anvil-agents
# implement, then:
make release-primaris-hot
# → docker buildx push controller, pin deploy.yaml, helm upgrade --wait
```

### B. Versioned release for GitOps (Argo)

```bash
git checkout master && git pull
# land the feature on master first
VERSION=v0.1.14 make release-primaris-fast   # or release-primaris for Kind gates
# review .hazyforge/.../deploy.yaml digests
git checkout -b chore/pin-v0.1.14-images
git add .hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml
git commit -m "chore(deploy): pin anvil-agents images to v0.1.14 digests"
git push -u origin HEAD
# open/merge PR; Argo syncs remote-helm
```

### C. Versioned release + immediate cluster apply

```bash
VERSION=v0.1.14 make release-primaris-fast RELEASE_DEPLOY=true
# still commit deploy.yaml so GitOps stays the source of truth
```

## CRD ownership

- Runtime CRDs live only in `anvil-agents` (`make manifests` → chart templates).
- Primaris must **not** re-deliver agent CRDs from its own charts.
- Uninstall keeps CRDs (`helm.sh/resource-policy: keep` + Argo `Prune=false`).
- Schema changes require a controller image that understands the new fields
  **and** a deploy that applied the updated CRD templates.

## Relation to older targets

| Legacy target | Role |
|---------------|------|
| `make release-local` | Images + chart only (no Primaris pin) |
| `make release-local-all` | Tag + publish + GitHub release |
| `make release-pin-deploy` | Pin deploy.yaml from existing lock |
| `make release-primaris*` | Preferred end-to-end Primaris path |

## Safety

- Versioned modes refuse dirty worktrees.
- Hot mode requires `--allow-dirty` (or Make’s hot target) when the tree is dirty.
- Production still prefers digest pins in Git; hot deploys rewrite local
  `deploy.yaml` — commit that pin when the cutover is permanent.
- Do not run the old embedded Primaris agent reconcilers alongside this chart.
