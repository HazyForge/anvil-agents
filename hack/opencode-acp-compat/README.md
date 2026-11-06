# Project-local OpenCode ACP catalog compatibility

Status: source patch prepared; dependency-free HTTP readiness tests are available.
The compiled native artifact, native SDK integration, CLI typecheck and prompt-free
exact selection proof are **not complete**. Build dependency installation was
blocked by automatic approval review. No live pilot may use this draft.

## Provenance and scope

`upstream.json` pins OpenCode 2.0.15 commit
`6f3639d82ed0760091792189b78f8eeb44f699b1`, the source archive SHA-256, the upstream
`bun.lock` SHA-256, and the original ACP/Standalone/model-handler source hashes.
The reference installed binary SHA-256 is
`b8ed44e893a3307cf23b4e0c83a4bb52b2c38d580fff33d3d9ec16477efb6077`.

`catalog-readiness.patch` changes only upstream `packages/cli/src/acp/service.ts`.
Copy `catalog-readiness.ts` into that same upstream directory after verifying the
manifest, then apply the patch. It waits for the exact enabled provider/model and
visible primary mode before accepting a catalog, reads defaults after readiness,
uses the connection/deadline AbortSignal for metadata GETs, drops the permanent
per-directory promise cache, and refreshes attached metadata before selection.
Wrong model/mode selections are refused before a native mutation. It does not
add a provider, credential, fallback, model request or authentication RPC.

The required values are operator-owned invocation environment, not HTTP input:
`ANVIL_OPENCODE_MODEL`, `ANVIL_OPENCODE_MODE`, and
`ANVIL_OPENCODE_CATALOG_TIMEOUT_MS` (1–30000 milliseconds). The intended temporary
route is `openai/gpt-4.1-mini`, `build`, and a 4500 ms native bound inside Anvil's
five-second setup bound. The original reviewed Anvil selection guard remains
unchanged and must validate the final actual selection acknowledgement.

## Reproducible build boundary

Build only in an isolated source/cache directory. The installed executable can
act as its embedded Bun 1.4.2 compiler with `BUN_BE_BUN=1`; no runtime installation
or global binary replacement is needed. The pending dependency step is the pinned
upstream CLI workspace install using `--frozen-lockfile --ignore-scripts --filter
@opencode/cli`, with an isolated HOME/cache and no user credential environment.
That step requires explicit approval after the automatic rejection. Do not run
an equivalent install through another route while approval is pending.

After approval, complete the affected CLI typecheck, build a project-only
`2.0.15-anvil-acp-catalog.2` artifact, record its SHA-256, and retain the original
binary/configuration. Obtain exact-head independent review. Then prove cold
native ACP startup and exact selection using only metadata in a private offline
namespace, including timeout/cancellation and no substitute model. Only after
those gates pass can the already approved, still-unused one-off pilot proceed.

## Tests and maintenance

Run the helper tests from this directory with an existing Bun runtime:
`bun test catalog-readiness.test.ts`. They exercise real loopback HTTP metadata,
delayed registration, disabled/stale metadata, revalidation, wrong model/mode,
bounded in-flight timeout and owner cancellation. They do not establish native
SDK/binary integration or provider entitlement. No model prompts are used.

Recheck the patch and all source/lock hashes when moving upstream versions;
never silently apply to another source or binary. Remove this compatibility
patch when upstream catalog readiness/cache handling satisfies these same
metadata cases and the unchanged Anvil guard. No public Protocol/Server API
schema, generated client, global installation, credentials or persistent
provider configuration are changed.

The initial runtime-preload prototype was rejected after a safe trace showed
no preload execution in this exact compiled artifact. It is not part of this
patch and must not be used as a claimed compatibility fix.

Revision 2 passes request cancellation/deadlines through argument two of the
pinned generated SDK methods for model, agent, default and command reads, and
checks cancellation again before accepting metadata. Both config-option and
legacy `session/set_mode` paths refresh attached metadata and use the same
operator-owned mode restriction before mutation. Original reviewer probes are
retained outside this directory and replayed unchanged.
