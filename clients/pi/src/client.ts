/**
 * The queqiao gateway's §6.4 endpoints, as the Pi extension calls them.
 * Every call is fail-safe: the router's worst outcome is "keep the model".
 */

export interface TurnRequest {
  session: string
  prompt: string
  agent?: string
  parentSession?: string
}

export interface TurnResponse {
  tier: string
  group: string
  reason?: string
}

export interface Feedback {
  session: string
  kind: "pr_created" | "pr_merged" | "pr_closed" | "manual_model_switch" | "thumbs_up" | "thumbs_down"
  value?: string
}

/** The budget §6.8 gives the /turn call, in ms. */
export const TURN_BUDGET_MS = 1500

export class QueqiaoClient {
  constructor(private readonly base: string) {}

  /** POST /v1/queqiao/turn; null on any failure, timeout included. */
  async turn(req: TurnRequest): Promise<TurnResponse | null> {
    const body: Record<string, unknown> = {
      harness: "pi",
      session: req.session,
      prompt: req.prompt,
      agent: req.agent ?? "main",
      store_hint: false,
    }
    if (req.parentSession !== undefined) body.parent_session = req.parentSession
    const out = (await this.post("/v1/queqiao/turn", body, TURN_BUDGET_MS)) as TurnResponse | null
    if (out === null || typeof out.tier !== "string" || typeof out.group !== "string") return null
    return out
  }

  /** POST /v1/queqiao/feedback; fire-and-forget. */
  async feedback(fb: Feedback): Promise<void> {
    await this.post("/v1/queqiao/feedback", fb).then(
      () => {},
      () => {},
    )
  }

  /** POST /v1/queqiao/lineage; marks a derived session; never throws. */
  async lineage(mark: { session: string; parentSession?: string; source: string }): Promise<void> {
    const body: Record<string, unknown> = { session: mark.session, source: mark.source }
    if (mark.parentSession !== undefined) body.parent_session = mark.parentSession
    await this.post("/v1/queqiao/lineage", body).then(
      () => {},
      () => {},
    )
  }

  private async post(path: string, body: unknown, timeoutMs?: number): Promise<unknown> {
    // the budget is raced on our side (not just AbortSignal.timeout, whose
    // clock a test rig cannot drive) so a hung gateway still returns null
    let abort: (() => void) | undefined
    const expired = timeoutMs !== undefined ? new Promise<never>((_, reject) => {
      const t = setTimeout(() => reject(new Error("queqiao: budget spent")), timeoutMs)
      abort = () => clearTimeout(t)
    }) : undefined
    try {
      const res = await (expired === undefined
        ? fetch(this.base + path, this.init(body))
        : Promise.race([fetch(this.base + path, this.init(body)), expired]))
      if (!res.ok) return null
      if (res.status === 204) return {}
      const text = await res.text()
      return text === "" ? {} : (JSON.parse(text) as unknown)
    } catch {
      return null
    } finally {
      abort?.()
    }
  }

  private init(body: unknown): RequestInit {
    return {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }
  }
}
