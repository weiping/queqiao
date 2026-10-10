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
    models: { "magpie/group/mb-fast": { id: "magpie/group/mb-fast" }, "magpie/group/mb-balanced": { id: "magpie/group/mb-balanced" } } as Record<string, { id: string }>,
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
    if (String(url).endsWith("/v1/bridge/turn")) {
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

describe("mbridge extension (SP7 end-of-turn review)", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    vi.stubEnv("MBRIDGE_URL", "http://gw")
  })
  afterEach(() => vi.unstubAllGlobals())

  it("agent_end posts one review carrying the turn's prompt and answer", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "what license is this repo?" })
    await f.fire("agent_end", {
      type: "agent_end",
      messages: [
        { role: "user", content: "what license is this repo?" },
        { role: "assistant", content: [{ type: "text", text: "It is " }, { type: "text", text: "MIT." }] },
      ],
    })
    await vi.waitFor(() => expect(bodies(fetchMock, "/v1/bridge/review").length).toBe(1))
    expect(bodies(fetchMock, "/v1/bridge/review")[0]).toMatchObject({
      session: "s-pi-1",
      harness: "pi",
      prompt: "what license is this repo?",
      answer: "It is MIT.",
    })
  })

  it("a manually pinned session posts no review", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hello" })
    f.fire("model_select", { source: "user", model: { id: "other/m" }, previousModel: { id: "magpie/group/mb-fast" } })
    await f.fire("agent_end", { type: "agent_end", messages: [{ role: "assistant", content: "ok" }] })
    expect(bodies(fetchMock, "/v1/bridge/review").length).toBe(0)
  })

  it("a performance-tier turn posts no review", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "performance", group: "group/mb-balanced" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hello" })
    await f.fire("agent_end", { type: "agent_end", messages: [{ role: "assistant", content: "ok" }] })
    expect(bodies(fetchMock, "/v1/bridge/review").length).toBe(0)
  })

  it("an unreachable gateway does not throw out of agent_end", async () => {
    const f = fakePi()
    let down = false
    fetchMock.mockImplementation(async (url: string) => {
      if (String(url).endsWith("/v1/bridge/turn")) {
        return new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast" }), { status: 200 })
      }
      if (down) throw new Error("connect ECONNREFUSED")
      return new Response("{}", { status: 200 })
    })
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hello" })
    down = true // the gateway goes away between the turn and its review
    const rejections: unknown[] = []
    const onRejection = (reason: unknown) => rejections.push(reason)
    process.on("unhandledRejection", onRejection)
    expect(() =>
      f.fire("agent_end", { type: "agent_end", messages: [{ role: "assistant", content: "ok" }] }),
    ).not.toThrow()
    await new Promise((r) => setTimeout(r, 20))
    process.off("unhandledRejection", onRejection)
    expect(rejections).toEqual([])
    expect(fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/v1/bridge/review"))).toBe(true)
  })
})

describe("mbridge extension", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    vi.stubEnv("MBRIDGE_URL", "http://gw")
  })
  afterEach(() => vi.unstubAllGlobals())

  it("subscribes to exactly the events of §6.8", async () => {
    const f = fakePi()
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    expect(f.registered().sort()).toEqual(
      ["agent_end", "before_agent_start", "before_provider_headers", "model_select", "session_start", "tool_call", "tool_result"].sort(),
    )
  })

  it("session_start keeps Pi's id; headers carry it to the gateway", async () => {
    const f = fakePi()
    f.state.header = { parentSession: "/home/u/.pi/agent/sessions/abc-123.jsonl" }
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
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
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "fork" }) // defensive: reason fork, no parent
    const lin = bodies(fetchMock, "/v1/bridge/lineage")
    expect(lin.length).toBe(1)
    expect(lin[0]).toMatchObject({ session: expect.any(String), source: "pi-fork" })
  })

  it("before_agent_start asks /turn and switches the model on a tier change", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "what license?" })
    const t = bodies(fetchMock, "/v1/bridge/turn")
    expect(t[0]).toMatchObject({ harness: "pi", session: "s-pi-1", prompt: "what license?", agent: "main", store_hint: false, cwd: expect.any(String) })
    expect(f.state.setModelCalls).toEqual(["magpie/group/mb-fast"])
  })

  it("the same tier twice switches only once; a later change switches again", async () => {
    const f = fakePi()
    gw(fetchMock, [
      { tier: "fast", group: "group/mb-fast" },
      { tier: "fast", group: "group/mb-fast" },
      { tier: "balanced", group: "group/mb-balanced" },
    ])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "three, this is much work" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/mb-fast", "magpie/group/mb-balanced"])
  })

  it("empty prompts skip /turn; a failed /turn never touches the model", async () => {
    const f = fakePi()
    fetchMock.mockRejectedValue(new Error("down"))
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "" })
    expect(bodies(fetchMock, "/v1/bridge/turn").length).toBe(0)
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hello" })
    expect(f.state.setModelCalls).toEqual([])
  })

  it("a missing model in the registry leaves the model alone", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "performance", group: "group/mb-perf" }]) // not in fake registry
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "hard thing" })
    expect(f.state.setModelCalls).toEqual([])
  })

  it("a derived session's first turn sends parent_session", async () => {
    const f = fakePi()
    f.state.header = { parentSession: "/sessions/parent-9.jsonl" }
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "go on" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "and again" })
    const t = bodies(fetchMock, "/v1/bridge/turn")
    expect(t[0].parent_session).toBe("parent-9")
    expect(t[1]).not.toHaveProperty("parent_session")
  })

  it("a manual model_select reports and stops auto-switching; our own does not", async () => {
    const f = fakePi()
    gw(fetchMock, [
      { tier: "fast", group: "group/mb-fast" },
      { tier: "balanced", group: "group/mb-balanced" },
    ])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/mb-fast"])

    // our own switch (model_select mirroring lastAutoModel): no feedback
    f.fire("model_select", { type: "model_select", source: "set", model: { id: "magpie/group/mb-fast" }, previousModel: undefined })
    expect(bodies(fetchMock, "/v1/bridge/feedback").length).toBe(0)

    // the user switches by hand: feedback once, auto-switch off
    f.fire("model_select", { type: "model_select", source: "set", model: { id: "other/m1" }, previousModel: { id: "magpie/group/mb-fast" } })
    expect(bodies(fetchMock, "/v1/bridge/feedback")).toMatchObject([
      { session: "s-pi-1", kind: "manual_model_switch", value: "magpie/group/mb-fast→other/m1" },
    ])
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two would switch tiers" })
    expect(f.state.setModelCalls).toEqual(["magpie/group/mb-fast"]) // pinned now

    // restore (a resumed session reloading its model) is neither
    f.fire("model_select", { type: "model_select", source: "restore", model: { id: "x/y" }, previousModel: undefined })
    expect(bodies(fetchMock, "/v1/bridge/feedback").length).toBe(1)
  })
})

describe("mbridge extension: feedback and subagents", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    vi.stubEnv("MBRIDGE_URL", "http://gw")
  })
  afterEach(() => vi.unstubAllGlobals())

  async function boot() {
    const f = fakePi()
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    return f
  }

  it("tool_result with a PR link in the text sends pr_created", async () => {
    const f = await boot()
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))
    f.fire("tool_result", {
      type: "tool_result",
      isError: false,
      content: [{ type: "text", text: "Opened https://github.com/weiping/magpie-bridge/pull/7 for review" }],
    })
    await new Promise((r) => setTimeout(r, 0))
    expect(bodies(fetchMock, "/v1/bridge/feedback")).toMatchObject([
      { session: "s-pi-1", kind: "pr_created", value: "https://github.com/weiping/magpie-bridge/pull/7" },
    ])
  })

  it("tool_result without a link, or an error result, sends nothing", async () => {
    const f = await boot()
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))
    f.fire("tool_result", { type: "tool_result", isError: false, content: [{ type: "text", text: "done" }] })
    f.fire("tool_result", { type: "tool_result", isError: true, content: [{ type: "text", text: "Opened https://github.com/a/b/pull/1" }] })
    await new Promise((r) => setTimeout(r, 0))
    expect(bodies(fetchMock, "/v1/bridge/feedback").length).toBe(0)
  })

  it("tool_call picks a tier for a fresh pi-subagents spawn and writes the model", async () => {
    const f = await boot()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    const input: Record<string, unknown> = { context: "fresh", task: "search the repo for todo markers" }
    await f.fire("tool_call", { type: "tool_call", toolName: "subagent", input })
    const t = bodies(fetchMock, "/v1/bridge/turn")
    expect(t[0]).toMatchObject({ session: "s-pi-1", prompt: "search the repo for todo markers", agent: "subagent", store_hint: false })
    expect(input.model).toBe("magpie/group/mb-fast")
  })

  it("tool_call uses the tier's real group id (performance is mb-perf, not mb-performance)", async () => {
    const f = await boot()
    gw(fetchMock, [{ tier: "performance", group: "group/mb-perf" }])
    const input: Record<string, unknown> = { context: "fresh", task: "design the migration" }
    await f.fire("tool_call", { type: "tool_call", toolName: "subagent", input })
    expect(input.model).toBe("magpie/group/mb-perf")
  })

  it("tool_call also handles dispatch_agent and the prompt/description fields", async () => {
    const f = await boot()
    gw(fetchMock, [{ tier: "balanced", group: "group/mb-balanced" }])
    const input: Record<string, unknown> = { context: "fresh", description: "fix the flaky test" }
    await f.fire("tool_call", { type: "tool_call", toolName: "dispatch_agent", input })
    expect(bodies(fetchMock, "/v1/bridge/turn")[0].prompt).toBe("fix the flaky test")
    expect(input.model).toBe("magpie/group/mb-balanced")
  })

  it("tool_call leaves fork-context spawns, preset models and other tools alone", async () => {
    const f = await boot()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    const forked: Record<string, unknown> = { context: "fork", task: "keep going in the fork" }
    await f.fire("tool_call", { type: "tool_call", toolName: "subagent", input: forked })
    const preset: Record<string, unknown> = { context: "fresh", task: "x", model: "magpie/some-model" }
    await f.fire("tool_call", { type: "tool_call", toolName: "subagent", input: preset })
    const other = { command: "ls" }
    await f.fire("tool_call", { type: "tool_call", toolName: "bash", input: other })
    expect(bodies(fetchMock, "/v1/bridge/turn").length).toBe(0)
    expect(forked).not.toHaveProperty("model")
    expect(preset.model).toBe("magpie/some-model")
  })

  it("tool_call with a failing /turn leaves the spawn without a model", async () => {
    const f = await boot()
    fetchMock.mockRejectedValue(new Error("down"))
    const input: Record<string, unknown> = { context: "fresh", task: "hard thing" }
    await f.fire("tool_call", { type: "tool_call", toolName: "subagent", input })
    expect(input).not.toHaveProperty("model")
  })
})

describe("mbridge extension (SP8)", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    vi.stubEnv("MBRIDGE_URL", "http://gw")
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  it("reports tool stats of the previous turn", async () => {
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    f.fire("tool_result", { toolName: "bash", isError: false, content: [] })
    f.fire("tool_result", { toolName: "read", isError: true, content: [] })
    f.fire("tool_result", { toolName: "bash", isError: true, content: [] })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two" })
    const t = bodies(fetchMock, "/v1/bridge/turn")
    expect(t[0].tool_calls).toBeUndefined()
    expect(t[1]).toMatchObject({ tool_calls: 3, tool_failures: 2 })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "three" })
    expect(bodies(fetchMock, "/v1/bridge/turn")[2]).toMatchObject({ tool_calls: 0, tool_failures: 0 })
  })

  it("defaults to mbridge on 3426", async () => {
    vi.unstubAllEnvs()
    const f = fakePi()
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    expect(String(fetchMock.mock.calls[0][0])).toBe("http://127.0.0.1:3426/v1/bridge/turn")
  })

  it("an unreachable mbridge keeps the model and says so in the status", async () => {
    const f = fakePi()
    const statuses: Array<[string, string | undefined]> = []
    ;(f.ctx as any).ui = { setStatus: (k: string, t: string | undefined) => statuses.push([k, t]) }
    fetchMock.mockRejectedValue(new Error("ECONNREFUSED"))
    ;(await import("../extensions/mbridge.js")).default(f.pi as never)
    f.fire("session_start", { type: "session_start", reason: "startup" })
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "one" })
    expect(f.state.setModelCalls).toEqual([])
    expect(statuses).toContainEqual(["mbridge", "mbridge: 未运行"])
    // back up: the status clears
    gw(fetchMock, [{ tier: "fast", group: "group/mb-fast" }])
    await f.fire("before_agent_start", { type: "before_agent_start", prompt: "two" })
    expect(statuses[statuses.length - 1]).toEqual(["mbridge", undefined])
  })
})
