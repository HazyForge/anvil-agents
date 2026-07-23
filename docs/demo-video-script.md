# Demo video script

Target 2:45–2:55. Record in English with clear narration. Do not include
credentials, private repository content, personal data, copyrighted music, or
third-party marks without permission.

## 0:00–0:20 — Problem and product

Show the README/architecture diagram and say:

> Developer agents compile, test, scan, and wait on long tasks that can swamp a
> laptop. Anvil Agents schedules each independent task as an isolated,
> observable Kubernetes Job with explicit policy, skills, resources, and
> durable state.

## 0:20–0:55 — Project history

Show `docs/build-week-2026.md` and a concise commit view. State that the embedded
AgentRun foundation predated Build Week. Call out only the eligible extensions:
standalone extraction, dependency severing, composition, OIDC read API, release
automation, provider-neutral adverse evidence, and public Kind test.

## 0:55–1:30 — GPT-5.6 product demo

Show a sanitized `AgentRun` manifest with the Codex backend and the exact
GPT-5.6 model identifier proven by the pre-recording evidence run; do not assume
the identifier is `gpt-5.6` merely because the CRD accepts it. Apply the
manifest to the prepared demo cluster. Show the resulting Job and Pod, then
live useful output and structured terminal status. Keep the verified model
field and run identity visible. Do not show a token, Secret, private prompt, or
unrelated cluster workload.

## 1:30–2:05 — Public no-build path

Run or show the final portion of `./hack/test-judge-kind.sh`. Explain that this
separate path needs no credentials and builds nothing. Show the two immutable
runs and `storage=retained`, then briefly inspect AgentRun, Job, Pod, and PVC.

## 2:05–2:35 — Codex collaboration and human decisions

Show the README collaboration section. Explain that Codex/GPT-5.6 accelerated
code archaeology, extraction, controller/API work, tests, security review,
release automation, and Kind debugging. State the human decisions: standalone
ownership, append-only runs, opaque app references, external policy authority,
separate read-only OIDC boundary, and one Job per run.

## 2:35–2:50 — Close

Show the immutable tag and public repository. End with the target audience and
impact: teams can use the whole cluster for independent agent workloads while
keeping scheduling, resource limits, state, and evidence explicit.

## Recording gate

- Total duration is under 3:00.
- Audio is intelligible and covers the project, Codex, and GPT-5.6.
- The video is public on YouTube.
- The shown revision matches the published repository/tag link.
- The GPT-5.6 run actually succeeds; do not substitute a static model field.
- No secrets, private URLs, copyrighted music, or unauthorized content appear.
