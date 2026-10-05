import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { QueqiaoClient } from "../src/client.js"

function lastCall(fetchMock: ReturnType<typeof vi.fn>) {
  const [url, init] = fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit]
  return { url, init, body: JSON.parse(String(init.body)) as Record<string, unknown> }
}

describe("QueqiaoClient", () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
  })
  afterEach(() => vi.unstubAllGlobals())

  it("tolerates a trailing slash in the base URL", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/qq-fast" }), { status: 200 }),
    )
    const c = new QueqiaoClient("http://gw/")
    await c.turn({ session: "s1", prompt: "hi" })
    expect(String(fetchMock.mock.calls[0][0])).toBe("http://gw/v1/queqiao/turn")
  })

  it("turn() carries cwd when given (§4.1 project criteria)", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/qq-fast" }), { status: 200 }),
    )
    const c = new QueqiaoClient("http://gw")
    await c.turn({ session: "s1", prompt: "hi", cwd: "/work/repo" })
    expect(lastCall(fetchMock).body.cwd).toBe("/work/repo")
  })

  it("turn() posts the pi harness shape and returns the tier", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ tier: "fast", group: "group/qq-fast", reason: "R5" }), { status: 200 }),
    )
    const c = new QueqiaoClient("http://gw")
    const out = await c.turn({ session: "s1", prompt: "what license?", parentSession: "s0" })
    expect(out).toEqual({ tier: "fast", group: "group/qq-fast", reason: "R5" })
    const { url, init, body } = lastCall(fetchMock)
    expect(url).toBe("http://gw/v1/queqiao/turn")
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
      new Response(JSON.stringify({ tier: "fast", group: "group/qq-fast" }), { status: 200 }),
    )
    const c = new QueqiaoClient("http://gw")
    await c.turn({ session: "s1", prompt: "hi" })
    expect(lastCall(fetchMock).body).not.toHaveProperty("parent_session")
  })

  it("turn() races a 1500ms budget and returns null on timeout", async () => {
    fetchMock.mockImplementationOnce(() => new Promise(() => {})) // hangs
    vi.useFakeTimers()
    const c = new QueqiaoClient("http://gw")
    const p = c.turn({ session: "s1", prompt: "hi" })
    const race = expect(p).resolves.toBeNull()
    await vi.advanceTimersByTimeAsync(1600)
    await race
    vi.useRealTimers()
  })

  it("turn() returns null on non-200 and on bad json", async () => {
    const c = new QueqiaoClient("http://gw")
    fetchMock.mockResolvedValueOnce(new Response("nope", { status: 500 }))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
    fetchMock.mockResolvedValueOnce(new Response("not json", { status: 200 }))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
    fetchMock.mockRejectedValueOnce(new Error("down"))
    expect(await c.turn({ session: "s1", prompt: "x" })).toBeNull()
  })

  it("feedback() is fire-and-forget and never throws", async () => {
    fetchMock.mockRejectedValueOnce(new Error("down"))
    const c = new QueqiaoClient("http://gw")
    await expect(c.feedback({ session: "s1", kind: "pr_created", value: "https://github.com/a/b/pull/1" })).resolves.toBeUndefined()
    const { url, body } = lastCall(fetchMock)
    expect(url).toBe("http://gw/v1/queqiao/feedback")
    expect(body).toMatchObject({ session: "s1", kind: "pr_created" })
  })

  it("lineage() posts and never throws", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    const c = new QueqiaoClient("http://gw")
    await expect(c.lineage({ session: "s1", source: "pi-fork" })).resolves.toBeUndefined()
    expect(lastCall(fetchMock).url).toBe("http://gw/v1/queqiao/lineage")
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
