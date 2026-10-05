import { describe, expect, it } from "vitest"

describe("pi-queqiao scaffold", () => {
  it("loads the extension without touching anything", async () => {
    const mod = await import("../extensions/queqiao.js")
    const ons: string[] = []
    const pi = { on: (name: string) => { ons.push(name); return () => {} } } as unknown as Parameters<typeof mod.default>[0]
    mod.default(pi)
    expect(ons).toEqual([])
  })
})
