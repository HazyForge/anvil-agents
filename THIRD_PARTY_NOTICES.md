# Third-party notices

Anvil Agents source is Apache-2.0 licensed. It can build optional runner images
that install third-party tools, and its public Kind test pulls a third-party
fixture image. Those projects remain governed by their own licenses and service
terms; this file does not replace their license texts or grant model/API access.

| Component | Pinned version or source | License / terms | Use in this repository |
| --- | --- | --- | --- |
| OpenAI Codex CLI | `@openai/codex@0.142.5`, [openai/codex](https://github.com/openai/codex) | Apache-2.0; OpenAI service terms apply when authenticated | Installed in the Codex runner and as a helper in some optional runners. |
| OpenCode | `1.18.3` with per-architecture SHA-256 pins, [anomalyco/opencode](https://github.com/anomalyco/opencode) | MIT; selected model/provider terms also apply | Installed in the optional OpenCode runner. The image verifies the binary archive and ships the version-pinned upstream license under `/usr/share/doc/opencode`. Not needed by the credential-free public Kind test. |
| Hermes Agent | `nousresearch/hermes-agent:v2026.7.7.2`, [NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent) | MIT; upstream model/provider terms also apply | Base of the optional Hermes runner. Not needed by the public Kind test. |
| OpenClaw | `openclaw@2026.6.11`, [openclaw/openclaw](https://github.com/openclaw/openclaw) | MIT; upstream provider terms also apply | Installed in the optional OpenClaw runner. Not needed by the public Kind test. |
| Grok Build CLI | `0.2.103` with per-architecture SHA-256 pins; notices snapshot `7cfcb20d2b50b0d18801a6c0af2e401c0e060894`; [xai-org/grok-build](https://github.com/xai-org/grok-build) | Apache-2.0 for first-party open-source code; bundled upstream third-party notices; xAI service terms apply when authenticated | Installed in the optional Grok Build runner. The image verifies the binary checksum and ships the upstream license plus root, tools, and vendored notices under `/usr/share/doc/grok-build`. Not needed by the public Kind test. |
| Pi coding agent | `@earendil-works/pi-coding-agent@0.80.6`, [earendil-works/pi](https://github.com/earendil-works/pi) | MIT; upstream provider terms also apply | Installed in the optional Pi runner. Not needed by the public Kind test. |
| Pi xAI OAuth extension | `pi-xai-oauth@1.3.1`, [BlockedPath/pi-xai-oauth](https://github.com/BlockedPath/pi-xai-oauth) | MIT; xAI terms apply when authenticated | Installed alongside Pi for optional xAI authentication. Not needed by the public Kind test. |
| BusyBox container | `docker.io/library/busybox@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028` (tag resolved from `1.37.0`), [BusyBox](https://busybox.net/) | GPL-2.0; official-image packaging has its own notices | Pulled directly from Docker Hub as the credential-free public Kind fixture; not copied into this repository or Anvil Agents images. |
| Kubernetes kubectl | `v1.34.0`, [kubernetes/kubernetes](https://github.com/kubernetes/kubernetes) | Apache-2.0 | Downloaded into runner images for optional cluster workflows. |
| Helm | `v3.21.2`, [helm/helm](https://github.com/helm/helm) | Apache-2.0 | Downloaded into Codex and Grok Build runners; used by the public Kind test. |
| GitHub CLI | distribution package, [cli/cli](https://github.com/cli/cli) | MIT; GitHub service terms apply | Installed in runner images for optional repository workflows. |

Runner images also contain Go, Node.js, Debian packages, base-image contents,
and Go/npm transitive dependencies. Their package metadata and upstream license
files remain authoritative. The controller build uses
`github.com/google/go-licenses/v2@v2.0.1` to collect the license and notice files
required by both statically linked Go binaries under
`/usr/share/licenses/anvil-agents`. The runner-side feedback binary imports only
the Go standard library and this repository's own Apache-2.0 packages. A signed
SBOM remains production supply-chain roadmap work; this repository does not
claim that separate provenance feature is complete.

No OpenCode, Hermes, OpenClaw, Grok, Pi, xAI, GitHub, Kubernetes, Helm, Docker,
or BusyBox trademark is claimed by Hazy Forge. The public Kind test
requires only the published Anvil Agents controller/chart and the official
BusyBox fixture.
