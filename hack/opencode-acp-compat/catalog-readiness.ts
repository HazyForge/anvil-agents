import type { OpenCodeClient } from "@opencode/client/promise"

export type CatalogRequirements = {
  model: string
  mode: string
  timeoutMs: number
}

export function catalogRequirements(): CatalogRequirements {
  const model = process.env.ANVIL_OPENCODE_MODEL ?? ""
  const mode = process.env.ANVIL_OPENCODE_MODE ?? ""
  const timeoutMs = Number(process.env.ANVIL_OPENCODE_CATALOG_TIMEOUT_MS)
  if (!/^[^\s/]+\/[^\s]+$/.test(model) || !mode || /[\s\0]/.test(mode) || model.length > 256 || mode.length > 256 || !Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 30_000) {
    throw new Error("Native ACP catalog requirements are invalid")
  }
  return { model, mode, timeoutMs }
}

// These are existing typed SDK metadata reads, never model inference or auth.
// A partial bootstrap registry cannot become a successful native catalog.
export async function readyCatalog(client: OpenCodeClient, cwd: string, required: CatalogRequirements, closed?: AbortSignal) {
  const timeout = AbortSignal.timeout(required.timeoutMs)
  const signal = closed ? AbortSignal.any([closed, timeout]) : timeout
  while (!signal.aborted) {
    const location = { directory: cwd }
    const [modelResult, agentResult] = await Promise.all([
      client.model.list({ location, signal }),
      client.agent.list({ location, signal }),
    ])
    const models = modelResult.data.filter((model) => model.enabled)
    const agents = agentResult.data.filter((agent) => agent.mode !== "subagent" && !agent.hidden)
    if (models.some((model) => `${model.providerID}/${model.id}` === required.model) && agents.some((agent) => agent.id === required.mode && agent.mode === "primary")) {
      return { models, agents, signal }
    }
    await new Promise<void>((resolve, reject) => {
      const abort = () => { clearTimeout(timer); signal.removeEventListener("abort", abort); reject(signal.reason) }
      const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve() }, 25)
      signal.addEventListener("abort", abort, { once: true })
      if (signal.aborted) abort()
    })
  }
  throw new Error("Native ACP catalog readiness unavailable")
}
