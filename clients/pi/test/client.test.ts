import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { MbridgeClient } from "../src/client.js"

function lastCall(fetchMock: ReturnType<typeof vi.fn>) {
  const [url, init] = fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit]
  return { url, init, body: JSON.parse(String(init.body)) as Record<string, unknown> }
}

describe("MbridgeClient", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
  })
  afterEach(() => vi.unstubAllGlobals())

  it("tolerates a trailing slash in the base URL", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast" }), { status: 200 }),
    )
    const c = new MbridgeClient("http://gw/")
    await c.turn({ session: "s1", prompt: "hi" })
    expect(String(fetchMock.mock.calls[0][0])).toBe("http://gw/v1/bridge/turn")
  })

  it("turn() carries cwd when given (§4.1 project criteria)", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast" }), { status: 200 }),
    )
    const c = new MbridgeClient("http://gw")
    await c.turn({ session: "s1", prompt: "hi", cwd: "/work/repo" })
    expect(lastCall(fetchMock).body.cwd).toBe("/work/repo")
  })

  it("turn() posts the pi harness shape and returns the tier", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast", reason: "R5" }), { status: 200 }),
    )
    const c = new MbridgeClient("http://gw")
    const out = await c.turn({ session: "s1", prompt: "what license?", parentSession: "s0" })
    expect(out).toEqual({ tier: "fast", group: "group/mb-fast", reason: "R5" })
    const { url, init, body } = lastCall(fetchMock)
    expect(url).toBe("http://gw/v1/bridge/turn")
    expect(init.method).toBe("POST")
    expect(body).toMatchObject({
      harness: "pi",
      session: "s1",
      prompt: "what license?",
      agent: "main",
      store_hint: false,
      parent_session: "s0",
    })
  })

  it("turn() omits parent_session when there is none", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast" }), { status: 200 }),
    )
    const c = new MbridgeClient("http://gw")
    await c.turn({ session: "s1", prompt: "hi" })
    expect(lastCall(fetchMock).body).not.toHaveProperty("parent_session")
  })

  it("turn() races a 1500ms budget and returns null on timeout", async () => {
    fetchMock.mockImplementationOnce(() => new Promise(() => {})) // hangs
    vi.useFakeTimers()
    const c = new MbridgeClient("http://gw")
    const p = c.turn({ session: "s1", prompt: "hi" })
    const race = expect(p).resolves.toBeNull()
    await vi.advanceTimersByTimeAsync(1600)
    await race
    vi.useRealTimers()
  })

  // SP10: mbridge says how long a turn may take; turn() waits that long
  for (const c of [
    { name: "3000 waits for a /turn that answers at 2000 ms", router: { turn_budget_ms: 3000 }, answerAt: 2000, want: "fast" },
    { name: "past 8000 is held to 8000", router: { turn_budget_ms: 60000 }, answerAt: 8500, want: null },
    { name: "under 1500 is held to 1500", router: { turn_budget_ms: 100 }, answerAt: 1000, want: "fast" },
    { name: "a router without it keeps 1500", router: {}, answerAt: 2000, want: null },
  ]) {
    it(`learnBudget(): turn_budget_ms ${c.name}`, async () => {
      fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(c.router), { status: 200 }))
      const c2 = new MbridgeClient("http://gw")
      await c2.learnBudget()
      expect(String(fetchMock.mock.calls[0][0])).toBe("http://gw/v1/bridge/router")
      vi.useFakeTimers()
      fetchMock.mockImplementationOnce(
        () => new Promise((r) => setTimeout(() => r(new Response(JSON.stringify({ tier: "fast", group: "group/mb-fast" }), { status: 200 })), c.answerAt)),
      )
      const p = c2.turn({ session: "s1", prompt: "hi" })
      await vi.advanceTimersByTimeAsync(c.answerAt + 100)
      const out = await p
      vi.useRealTimers()
      expect(out === null ? null : out.tier).toBe(c.want)
    })
  }

  it("learnBudget() on a gateway that is down keeps 1500 and never throws", async () => {
    fetchMock.mockRejectedValueOnce(new Error("ECONNREFUSED"))
    const c = new MbridgeClient("http://gw")
    await expect(c.learnBudget()).resolves.toBeUndefined()
    vi.useFakeTimers()
    fetchMock.mockImplementationOnce(() => new Promise(() => {}))
    const p = c.turn({ session: "s1", prompt: "hi" })
    const race = expect(p).resolves.toBeNull()
    await vi.advanceTimersByTimeAsync(1600)
    await race
    vi.useRealTimers()
  })

  it("turn() returns null on non-200 and on bad json", async () => {
    const c = new MbridgeClient("http://gw")
    fetchMock.mockResolvedValueOnce(new Response("nope", { status: 500 }))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
    fetchMock.mockResolvedValueOnce(new Response("not json", { status: 200 }))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
    fetchMock.mockRejectedValueOnce(new Error("down"))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
  })

  it("feedback() is fire-and-forget and never throws", async () => {
    fetchMock.mockRejectedValueOnce(new Error("down"))
    const c = new MbridgeClient("http://gw")
    await expect(c.feedback({ session: "s1", kind: "pr_created", value: "https://github.com/a/b/pull/1" })).resolves.toBeUndefined()
    const { url, body } = lastCall(fetchMock)
    expect(url).toBe("http://gw/v1/bridge/feedback")
    expect(body).toMatchObject({ session: "s1", kind: "pr_created" })
  })

  it("lineage() posts and never throws", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    const c = new MbridgeClient("http://gw")
    await expect(c.lineage({ session: "s1", source: "pi-fork" })).resolves.toBeUndefined()
    expect(lastCall(fetchMock).url).toBe("http://gw/v1/bridge/lineage")
  })
})

describe("parentIdFromHeader", () => {
  it("extracts the session id from timestamp-prefixed pi 1.0.2 filenames", async () => {
    const { parentIdFromHeader } = await import("../src/session.js")
    expect(
      parentIdFromHeader({
        parentSession:
          "/Users/u/.pi/agent/sessions/-w-/2026-10-05T08-41-23-729Z_01a10b39-8e11-7476-bcb8-94b98e283b93.jsonl",
      }),
    ).toBe("01a10b39-8e11-7476-bcb8-94b98e283b93")
  })

  it("still handles plain <id>.jsonl names (pi 1.0.0, S12's shape)", async () => {
    const { parentIdFromHeader } = await import("../src/session.js")
    expect(parentIdFromHeader({ parentSession: "/sessions/abc-123.jsonl" })).toBe("abc-123")
  })
})
