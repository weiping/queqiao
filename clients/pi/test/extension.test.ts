import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

// The Pi object, mocked to the shapes S4/S12 recorded: ctx.sessionManager
// (getSessionId, getHeader), ctx.modelRegistry.find, pi.setModel,
// pi.on(event, handler). Events are driven by calling the stored handler.
type Handler = (event: any, ctx: any) => unknown

function fakePi() {
  const handlers = new Map<string, Handler>()
  const state = {
    sessionId: "s-pi-1" as string | undefined,
    header: {} as Record<string, unknown>,
    models: { "magpie/group/qq-fast": { id: "magpie/group/qq-fast" }, "magpie/group/qq-balanced": { id: "magpie/group/qq-balanced" } } as Record<string, { id: string }>,
    setModelCalls: [] as string[],
    setModelOk: true,
  }
  const pi = {
    on: vi.fn((name: string, handler: Handler) => {
      handlers.set(name, handler)
      return () => handlers.delete(name)
    }),
    setModel: vi.fn(async (model: { id: string }) => {
      state.setModelCalls.push(model.id)
      return state.setModelOk
    }),
  }
  const ctx = {
    sessionManager: {
      getSessionId: () => state.sessionId,
      getHeader: () => state.header,
    },
    modelRegistry: {
      find: (provider: string, model: string) => state.models[`${provider}/${model}`],
    },
  }
  return {
    pi,
    ctx,
    state,
    fire: (name: string, event: any) => handlers.get(name)?.(event, ctx),
    registered: () => [...handlers.keys()],
  }
}

function gw(fetchMock: ReturnType<typeof vi.fn>, turns: Array<{ tier: string; group: string }>) {
  let n = 0
  fetchMock.mockImplementation(async (url: string) => {
    if (String(url).endsWith("/v1/queqiao/turn")) {
      const t = turns[Math.min(n, turns.length - 1)]
      n++
      return new Response(JSON.stringify(t), { status: 200 })
    }
    return new Response("{}", { status: 200 })
  })
}

const bodies = (fetchMock: ReturnType<typeof vi.fn>, path: string) =>
  fetchMock.mock.calls
    .filter((c) => String(c[0]).endsWith(path))
    .map((c) => JSON.parse(String((c as unknown[])[1] ? (c[1] as RequestInit).body : "")) as Record<string, unknown>)

describe("queqiao extension", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    vi.stubEnv("QUEQIAO_URL", "http://gw")
  })
  afterEach(() => vi.unstubAllGlobals())

  it("subscribes to exactly the events of §6.8", async () => {
    const f = fakePi()
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    expect(f.registered().sort()).toEqual(
      ["before_agent_start", "before_provider_headers", "model_select", "session_start", "tool_call", "tool_result"].sort(),
    )
  })

  it("session_start keeps Pi's id; headers carry it to the gateway", async () => {
    const f = fakePi()
    f.state.header = { parentSession: "/home/u/.pi/agent/sessions/abc-123.jsonl" }
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    // the handler mutates the event in place (Pi's contract)
    const ev = { type: "before_provider_headers", headers: {} as Record<string, string> }
    f.fire("before_provider_headers", ev)
    expect(ev.headers["X-Magpie-Session"]).toBe("s-pi-1")
  })

  it("session_start falls back to a generated id and /lineage without a parent", async () => {
    const f = fakePi()
    f.state.sessionId = undefined
    f.state.header = {}
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "fork" }) // defensive: reason fork, no parent
    const lin = bodies(fetchMock, "/v1/queqiao/lineage")
    expect(lin.length).toBe(1)
    expect(lin[0]).toMatchObject({ session: expect.any(String), source: "pi-fork" })
  })

  it("before_agent_start asks /turn and switches the model on a tier change", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/qq-fast" }])
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "what license?" })
    const t = bodies(fetchMock, "/v1/queqiao/turn")
    expect(t[0]).toMatchObject({ harness: "pi", session: "s-pi-1", prompt: "what license?", agent: "main", store_hint: false })
    expect(f.state.setModelCalls).toEqual(["magpie/group/qq-fast"])
  })

  it("the same tier twice switches only once; a later change switches again", async () => {
    const f = fakePi()
    gw(fetchMock, [
      { tier: "fast", group: "group/qq-fast" },
      { tier: "fast", group: "group/qq-fast" },
      { tier: "balanced", group: "group/qq-balanced" },
    ])
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "three, this is much work" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/qq-fast", "magpie/group/qq-balanced"])
  })

  it("empty prompts skip /turn; a failed /turn never touches the model", async () => {
    const f = fakePi()
    fetchMock.mockRejectedValue(new Error("down"))
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "" })
    expect(bodies(fetchMock, "/v1/queqiao/turn").length).toBe(0)
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hello" })
    expect(f.state.setModelCalls).toEqual([])
  })

  it("a missing model in the registry leaves the model alone", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "performance", group: "group/qq-perf" }]) // not in fake registry
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hard thing" })
    expect(f.state.setModelCalls).toEqual([])
  })

  it("a derived session's first turn sends parent_session", async () => {
    const f = fakePi()
    f.state.header = { parentSession: "/sessions/parent-9.jsonl" }
    gw(fetchMock, [{ tier: "fast", group: "group/qq-fast" }])
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "go on" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "and again" })
    const t = bodies(fetchMock, "/v1/queqiao/turn")
    expect(t[0].parent_session).toBe("parent-9")
    expect(t[1]).not.toHaveProperty("parent_session")
  })

  it("a manual model_select reports and stops auto-switching; our own does not", async () => {
    const f = fakePi()
    gw(fetchMock, [
      { tier: "fast", group: "group/qq-fast" },
      { tier: "balanced", group: "group/qq-balanced" },
    ])
    ;(await import("../extensions/queqiao.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/qq-fast"])

    // our own switch (model_select mirroring lastAutoModel): no feedback
    f.fire("model_select", { type: "model_select", source: "set", model: { id: "magpie/group/qq-fast" }, previousModel: undefined })
    expect(bodies(fetchMock, "/v1/queqiao/feedback").length).toBe(0)

    // the user switches by hand: feedback once, auto-switch off
    f.fire("model_select", { type: "model_select", source: "set", model: { id: "other/m1" }, previousModel: { id: "magpie/group/qq-fast" } })
    expect(bodies(fetchMock, "/v1/queqiao/feedback")).toMatchObject([
      { session: "s-pi-1", kind: "manual_model_switch", value: "magpie/group/qq-fast→other/m1" },
    ])
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two would switch tiers" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/qq-fast"]) // pinned now

    // restore (a resumed session reloading its model) is neither
    f.fire("model_select", { type: "model_select", source: "restore", model: { id: "x/y" }, previousModel: undefined })
    expect(bodies(fetchMock, "/v1/queqiao/feedback").length).toBe(1)
  })
})
