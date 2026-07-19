# Judging Anvil Agents

Anvil Agents is being prepared as a Developer Tools submission. The public test
below is a free, credential-free validation of the released operator; it is
deliberately separate from the authenticated GPT-5.6 evidence that must be
captured before the final video and submission.

## Supported test host

- Linux amd64
- Docker with at least 2 CPUs and 3 GiB available
- Kind, kubectl, and Helm 3.14 or newer
- Outbound HTTPS access to Docker Hub and GHCR
- Approximately five minutes on a typical broadband connection

The submitted release targets Kubernetes 1.30 or newer. Arm64 and older
Kubernetes versions are not certified for this entry.

## Free no-build test

Check out the immutable submission revision listed on Devpost, then run:

```bash
./hack/test-judge-kind.sh
```

The normal test refuses to reuse an existing cluster with the same name. Its
explicit cleanup mode deletes a cluster only when the isolated kubeconfig,
ownership marker, and labeled judge namespace all identify a cluster created by
this script. Choose another name if needed:

```bash
ANVIL_AGENTS_JUDGE_CLUSTER=anvil-agents-judge-2 \
  ./hack/test-judge-kind.sh
```

It pulls OCI chart `0.1.1`, verifies chart digest
`sha256:16a867c09b21287029797e43ba42cb633277ed1d3eb8d764dc3516f00a4c970c`,
and pins the controller to the release-lock digest used by the published
linux/amd64 release. It then proves:

1. all nine v0.1.1 CRDs and the controller install successfully;
2. profile, harness, and skill composition resolves to immutable digests;
3. an `AgentRun` creates an isolated Kubernetes Job;
4. the custom harness validates content from its mounted prompt, context, and
   composed skill file;
5. structured status reaches the terminal `Succeeded` phase; and
6. a second append-only run reads the first run's PVC marker; and
7. the API rejects an attempted mutation to the completed run's spec.

This checked-in candidate tests the public v0.1.1 runtime contract, not later
unreleased source changes. Before Devpost submission, either publish and pin a
release from the immutable submission revision or keep all runnable claims
strictly limited to features present in v0.1.1. The final repository tag,
release artifacts, video, description, and evidence ledger must describe the
same selected scope.

The success footer includes:

```text
Public Kind judge test passed
  runs: judge-write-001 -> judge-read-002
  proof: phase=Succeeded backend=custom storage=retained
  kubeconfig: /tmp/anvil-agents-judge-...
  inspect: KUBECONFIG=... kubectl ...
  cleanup: ANVIL_AGENTS_JUDGE_CLUSTER=... ./hack/test-judge-kind.sh --cleanup
```

Inspect the still-running cluster:

```bash
export KUBECONFIG=/tmp/anvil-agents-judge-${UID}-anvil-agents-judge.kubeconfig
kubectl --context kind-anvil-agents-judge \
  --namespace anvil-agents-judge get agentruns,jobs,pods,pvc
kubectl --context kind-anvil-agents-judge \
  --namespace anvil-agents-judge get agentrun judge-read-002 -o yaml
```

Cleanup is explicit and targets only the selected Kind cluster:

```bash
./hack/test-judge-kind.sh --cleanup
```

## GPT-5.6 evidence

The no-build test does not make an OpenAI call and never claims otherwise. The
submission video and Devpost description must show a separate authenticated
Codex-backed `AgentRun` whose `spec.harness.backend.codex.model` contains the
verified GPT-5.6 runtime model ID, plus the created Job and Pod, useful model
output, and terminal structured status. The final evidence record belongs in
[the Build Week ledger](docs/build-week-2026.md) before submission. Judges do
not need the entrant's OpenAI credential to run the free operator test.

## Troubleshooting

- `Docker ... daemon is unavailable`: start Docker and rerun with a fresh
  cluster name.
- GHCR or Docker Hub pull errors: verify outbound HTTPS access and retry; the
  test does not need registry authentication.
- An existing cluster-name error: set `ANVIL_AGENTS_JUDGE_CLUSTER` to a unique
  name. The script will not mutate the existing cluster.
- A failed run is retained for inspection. Use `kubectl describe` on the
  `AgentRun`, Job, and Pod before invoking explicit cleanup.

The repository is Apache-2.0 licensed. See [Third-party notices](THIRD_PARTY_NOTICES.md)
for the optional runner and judge-fixture dependencies.
