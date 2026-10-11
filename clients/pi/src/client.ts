/**
 * The mbridge gateway's §6.4 endpoints, as the Pi extension calls them.
 * Every call is fail-safe: the router's worst outcome is "keep the model".
 */

export interface TurnRequest {
  session: string
  prompt: string
  agent?: string
  /** Project dir; lets the gateway load .mbridge/router.json criteria. */
  cwd?: string
  parentSession?: string
  /** The main session's tool results since the last turn (SP8). */
  toolCalls?: number
  toolFailures?: number
  /** The assistant's reply before this prompt (SP11). */
  previousAnswer?: string
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

/** The end-of-turn review's body (SP7 §3.3). */
export interface ReviewBody {
  session: string
  turnId?: string
  prompt: string
  answer: string
  toolCalls?: number
  toolFailures?: number
}

/** The budget §6.8 gives the /turn call, in ms, until mbridge says otherwise. */
export const TURN_BUDGET_MS = 1500

/** How long the /router probe may take (SP10). */
const PROBE_MS = 500

/** SP10: mbridge's turn_budget_ms, held to [1500, 8000]; null when absent. */
export function clampBudget(ms: unknown): number | null {
  return typeof ms === "number" && Number.isFinite(ms) ? Math.min(8000, Math.max(1500, Math.round(ms))) : null
}

export class MbridgeClient {
  constructor(base: string) {
    // tolerate a trailing slash: "//v1/..." would hit the gateway's path
    // cleaning and turn a POST into a redirect
    this.base = base.replace(/\/+$/, "")
  }

  private readonly base: string
  private turnBudgetMs = TURN_BUDGET_MS
  private budgetLearned = false

  /** Whether mbridge has answered a budget probe yet (SP10). */
  get learned(): boolean {
    return this.budgetLearned
  }

  /** GET /v1/bridge/router (SP10): how long a turn may take. Gives up after
   *  500 ms and never throws; an mbridge that names no budget means 1500. */
  async learnBudget(): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined
    const expired = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("mbridge: probe timed out")), PROBE_MS)
    })
    try {
      const res = await Promise.race([fetch(this.base + "/v1/bridge/router", { method: "GET" }), expired])
      if (!res.ok) return
      const b = clampBudget(((await res.json()) as { turn_budget_ms?: unknown }).turn_budget_ms)
      this.turnBudgetMs = b ?? TURN_BUDGET_MS
      this.budgetLearned = true
    } catch {
      // mbridge down, hung or an old one: keep the budget we have
    } finally {
      clearTimeout(timer)
    }
  }

  /** POST /v1/bridge/turn; null on any failure, timeout included. */
  async turn(req: TurnRequest): Promise<TurnResponse | null> {
    const body: Record<string, unknown> = {
      harness: "pi",
      session: req.session,
      prompt: req.prompt,
      agent: req.agent ?? "main",
      store_hint: false,
    }
    if (req.cwd !== undefined) body.cwd = req.cwd
    if (req.parentSession !== undefined) body.parent_session = req.parentSession
    if (req.toolCalls !== undefined) body.tool_calls = req.toolCalls
    if (req.toolFailures !== undefined) body.tool_failures = req.toolFailures
    if (req.previousAnswer !== undefined && req.previousAnswer !== "") body.previous_answer = clientCut(req.previousAnswer)
    const out = (await this.post("/v1/bridge/turn", body, this.turnBudgetMs)) as TurnResponse | null
    if (out === null || typeof out.tier !== "string" || typeof out.group !== "string") return null
    return out
  }

  /**
   * POST /v1/bridge/review; fire-and-forget (SP7 §3.5). The answer has
   * already been delivered, so nothing waits on the gateway here.
   */
  review(body: ReviewBody): void {
    const payload: Record<string, unknown> = {
      harness: "pi",
      session: body.session,
      prompt: body.prompt,
      answer: body.answer,
    }
    if (body.turnId !== undefined) payload.turn_id = body.turnId
    if (body.toolCalls !== undefined) payload.tool_calls = body.toolCalls
    if (body.toolFailures !== undefined) payload.tool_failures = body.toolFailures
    try {
      void fetch(this.base + "/v1/bridge/review", this.init(payload)).catch(() => {})
    } catch {
      // a review that never arrives just means no R3-review next turn
    }
  }

  /** POST /v1/bridge/feedback; fire-and-forget. */
  async feedback(fb: Feedback): Promise<void> {
    await this.post("/v1/bridge/feedback", fb).then(
      () => {},
      () => {},
    )
  }

  /** POST /v1/bridge/lineage; marks a derived session; never throws. */
  async lineage(mark: { session: string; parentSession?: string; source: string }): Promise<void> {
    const body: Record<string, unknown> = { session: mark.session, source: mark.source }
    if (mark.parentSession !== undefined) body.parent_session = mark.parentSession
    await this.post("/v1/bridge/lineage", body).then(
      () => {},
      () => {},
    )
  }

  private async post(path: string, body: unknown, timeoutMs?: number): Promise<unknown> {
    // the budget is raced on our side (not just AbortSignal.timeout, whose
    // clock a test rig cannot drive) so a hung gateway still returns null
    let abort: (() => void) | undefined
    const expired = timeoutMs !== undefined ? new Promise<never>((_, reject) => {
      const t = setTimeout(() => reject(new Error("mbridge: budget spent")), timeoutMs)
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

/** SP11: at most 4000 characters of a previous answer, the first 1000 and
 *  the last 3000; mbridge cuts further. */
export function clientCut(s: string): string {
  const cs = Array.from(s)
  return cs.length > 4000 ? cs.slice(0, 1000).join("") + "\n…\n" + cs.slice(-3000).join("") : s
}
