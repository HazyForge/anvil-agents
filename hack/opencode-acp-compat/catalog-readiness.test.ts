import { afterEach, expect, test } from "bun:test"
import type { OpenCodeClient } from "@opencode/client/promise"
import { readyCatalog } from "./catalog-readiness"

const servers: ReturnType<typeof Bun.serve>[] = []
afterEach(() => { for (const server of servers.splice(0)) server.stop(true) })
const required = { model: "openai/approved-synthetic-model", mode: "build", timeoutMs: 300 }

function fixture() {
  const state = { ready: false, mode: "build", hidden: false, enabled: true, slow: 0, calls: [] as string[] }
  const server = Bun.serve({ hostname: "127.0.0.1", port: 0, async fetch(request) {
    const path = new URL(request.url).pathname
    state.calls.push(request.method + " " + path)
    if (state.slow) await Bun.sleep(state.slow)
    const data = path === "/model"
      ? [{ providerID: "opencode", id: "foreign-bootstrap", enabled: true }, ...(state.ready ? [{ providerID: "openai", id: "approved-synthetic-model", enabled: state.enabled }] : [])]
      : [{ id: state.mode, mode: "primary", hidden: state.hidden }]
    return Response.json({ data })
  } })
  servers.push(server)
  const read = async (path: string, options: { signal?: AbortSignal }) => (await fetch(`http://127.0.0.1:${server.port}${path}`, { signal: options.signal })).json()
  // Real loopback transport at the existing injected SDK boundary. This proves
  // the readiness helper; native SDK/binary integration remains a separate gate.
  const client = { model: { list: (_input: unknown, options: { signal?: AbortSignal } = {}) => read("/model", options) }, agent: { list: (_input: unknown, options: { signal?: AbortSignal } = {}) => read("/agent", options) } } as unknown as OpenCodeClient
  return { state, client }
}

test("cold bootstrap waits for the exact enabled model", async () => {
  const { state, client } = fixture()
  const delayed = setTimeout(() => { state.ready = true }, 75)
  try {
    const result = await readyCatalog(client, "/synthetic", required)
    expect(result.models.some(model => `${model.providerID}/${model.id}` === required.model)).toBe(true)
    expect(state.calls.filter(call => call === "GET /model").length).toBeGreaterThan(1)
    expect(state.calls.every(call => call.startsWith("GET "))).toBe(true)
  } finally { clearTimeout(delayed) }
})

test("disabled model metadata recovers only after enablement", async () => {
  const { state, client } = fixture()
  state.ready = true; state.enabled = false
  const delayed = setTimeout(() => { state.enabled = true }, 60)
  try {
    const result = await readyCatalog(client, "/synthetic", required)
    expect(result.models.find(model => model.id === "approved-synthetic-model")?.enabled).toBe(true)
    expect(state.calls.filter(call => call === "GET /model").length).toBeGreaterThan(1)
  } finally { clearTimeout(delayed) }
})

test("a later attachment revalidates instead of retaining an old snapshot", async () => {
  const { state, client } = fixture(); state.ready = true
  await readyCatalog(client, "/synthetic", required)
  state.ready = false
  await expect(readyCatalog(client, "/synthetic", { ...required, timeoutMs: 80 })).rejects.toThrow()
  expect(state.calls.filter(call => call === "GET /model").length).toBeGreaterThan(2)
})

test("wrong provider/model refuses bootstrap substitution", async () => {
  const { client } = fixture()
  await expect(readyCatalog(client, "/synthetic", { ...required, timeoutMs: 80 })).rejects.toThrow()
})

test("wrong or hidden mode cannot satisfy readiness", async () => {
  for (const hidden of [false, true]) {
    const { state, client } = fixture(); state.ready = true; state.hidden = hidden
    state.mode = hidden ? "build" : "plan"
    await expect(readyCatalog(client, "/synthetic", { ...required, timeoutMs: 60 })).rejects.toThrow()
  }
})

test("the deadline bounds an in-flight metadata response", async () => {
  const { state, client } = fixture(); state.slow = 500
  const started = performance.now()
  await expect(readyCatalog(client, "/synthetic", { ...required, timeoutMs: 60 })).rejects.toThrow()
  expect(performance.now() - started).toBeLessThan(350)
})

test("owner cancellation stops pending metadata and further reads", async () => {
  const { state, client } = fixture()
  const closed = new AbortController()
  const pending = readyCatalog(client, "/synthetic", { ...required, timeoutMs: 1000 }, closed.signal)
  setTimeout(() => closed.abort(), 40)
  const started = performance.now()
  await expect(pending).rejects.toThrow()
  expect(performance.now() - started).toBeLessThan(350)
  const calls = state.calls.length; await Bun.sleep(60)
  expect(state.calls.length).toBe(calls)
})
