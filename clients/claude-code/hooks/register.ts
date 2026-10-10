import { atom, read, update, type Register } from 'claude-code'

/**
 * magpie-bridge: routes each Claude Code turn across mbridge's tiers
 * (fast / balanced / performance) by asking the mbridge gateway at
 * turn.start and rewriting the model at turn.step. Fail-safe throughout:
 * the worst outcome of any failure is a request left as group/mbridge,
 * which the gateway's own fallback mode routes.
 *
 * State lives in $.state (atoms), not module variables, so it survives
 * hot reloads during development. /clear, /resume and /branch reset
 * $.state to defaults; classic.SessionStart refires after each and
 * re-marks derived sessions, and every other value here is safe to
 * rebuild as the session goes on.
 */

type Tier = 'fast' | 'balanced' | 'performance'
type Turn = { turnId: string; tier: Tier; group: string }

const stTurn = atom({ plugin: 'magpie-bridge', key: 'turn' }, null as Turn | null)
const stMainTier = atom({ plugin: 'magpie-bridge', key: 'mainTier' }, null as Tier | null)
const stAgentTier = atom({ plugin: 'magpie-bridge', key: 'agentTier' }, {} as Record<string, Tier>)
const stToolStats = atom({ plugin: 'magpie-bridge', key: 'toolStats' }, { calls: 0, failures: 0 })
const stDerived = atom({ plugin: 'magpie-bridge', key: 'derived' }, { is: false, checked: false })
const stPlanMode = atom({ plugin: 'magpie-bridge', key: 'planMode' }, false)
const stCwd = atom({ plugin: 'magpie-bridge', key: 'cwd' }, null as string | null)
const stStored = atom({ plugin: 'magpie-bridge', key: 'stored' }, false)
// SP7 §3.5: this turn's user words, kept for the end-of-turn review
const stPrompt = atom({ plugin: 'magpie-bridge', key: 'prompt' }, null as string | null)

// §5.8: the same hash on both the storing side and the looking-up side
function fnv1a(s: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(16)
}

type SessionMessageLite = { role: 'user' | 'assistant'; text: string }

async function firstUserHash($: any): Promise<string | null> {
  try {
    const messages = (await $.session.messages()) as SessionMessageLite[]
    for (const m of messages) {
      if (m.role === 'user' && m.text !== '') return fnv1a(m.text)
    }
  } catch {
    // messages unavailable: nothing to hash
  }
  return null
}

/** The parent session id for a derived session, from $.store (§5.8). */
async function parentFromStore($: any, hash: string, own: string): Promise<string | undefined> {
  try {
    const hit = (await $.store.get('qq:first:' + hash)) as { session?: string } | undefined
    if (hit !== undefined && typeof hit.session === 'string' && hit.session !== own) return hit.session
  } catch {
    // store unavailable: treated as a fresh session
  }
  return undefined
}

const ROUTING_GROUP = 'group/mbridge'
const TURN_BUDGET_MS = 1500

// §5.5 step 3 / R1: subagent types with a fixed tier (matches the Go
// side's fixed_agents).
const FIXED_AGENT: Record<string, Tier> = {
  Explore: 'fast',
  'statusline-setup': 'fast',
  'claude-code-guide': 'fast',
  explorer: 'fast',
  Plan: 'performance',
}

// §5.5 step 5: tier → the alias §4.5's env vars resolve to a tier group
const TIER_ALIAS: Record<Tier, string> = { fast: 'haiku', balanced: 'sonnet', performance: 'opus' }

// §5.8: a pinned agent's tier → its tier group (router init's §4.4 ids)
const TIER_GROUP: Record<Tier, string> = { fast: 'group/mb-fast', balanced: 'group/mb-balanced', performance: 'group/mb-perf' }
const tierGroup = (tier: Tier): string => TIER_GROUP[tier]

// "group/mbridge[1m]" → { base: "group/mbridge", suffix: "[1m]" }
const splitSuffix = (model: string): { base: string; suffix: string } => {
  const m = /^(.*?)(\[[^\]]*\])$/.exec(model)
  return m ? { base: m[1], suffix: m[2] } : { base: model, suffix: '' }
}

export const register: Register = (on, options) => {
  const gateway = String(options.gateway_url ?? 'http://127.0.0.1:3426').replace(/\/$/, '')

  on('session.start', async ($, e, next) => {
    if (typeof e.cwd === 'string' && e.cwd !== '') await update($, stCwd, () => e.cwd)
    // one reachability probe; being down changes nothing else
    try {
      const res = await $.http.fetch(gateway + '/v1/bridge/router', { method: 'GET' })
      if (!res.ok) throw new Error(String(res.status))
    } catch {
      try {
        await $.ui.status('mbridge: 未运行')
      } catch {
        // ui.status refused; nothing more to do
      }
    }
    return next(e)
  })

  // /fork and /branch both report source "fork" (S11); /clear, /resume and
  // /branch reset $.state and this refires, so re-mark on every event
  on('classic.SessionStart', async ($, e, next) => {
    await update($, stDerived, () => ({ is: e.source === 'fork', checked: false }))
    return next(e)
  })

  // plan detection (S1's proven read); empty-text turns keep the last value
  on('classic.UserPromptSubmit', async ($, e, next) => {
    await update($, stPlanMode, () => e.permission_mode === 'plan')
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (e.text !== '') {
      await update($, stPrompt, () => e.text)
      const session = await $.session.id()
      const stats = await read($, stToolStats)
      const planMode = await read($, stPlanMode)
      const cwd = await read($, stCwd)
      // a derived session looks its parent up once (§5.8)
      let parentSession: string | undefined
      const derived = await read($, stDerived)
      if (derived.is && !derived.checked) {
        await update($, stDerived, (d) => ({ ...d, checked: true }))
        const hash = await firstUserHash($)
        if (hash !== null) parentSession = await parentFromStore($, hash, session)
      }
      const decided = await decideTurn($, gateway, {
        harness: 'claude-code',
        session,
        prompt: e.text,
        agent: 'main',
        plan_mode: planMode,
        ...(cwd !== null ? { cwd } : {}),
        ...(parentSession !== undefined ? { parent_session: parentSession } : {}),
        tool_calls: stats.calls,
        tool_failures: stats.failures,
        store_hint: false,
      })
      if (decided !== null) {
        await update($, stTurn, () => ({ turnId: e.turnId, tier: decided.tier, group: decided.group }))
        await update($, stMainTier, () => decided.tier)
        try {
          await $.ui.status(`mbridge: ${decided.tier} · ${decided.reason}`)
        } catch {
          // status is cosmetic
        }
      } else {
        await update($, stTurn, () => null)
      }
      // reported (or failed reporting) — start the next turn from zero
      await update($, stToolStats, () => ({ calls: 0, failures: 0 }))
    }
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    const r = await next(e)
    if (e.agentId === undefined) {
      // the engine hands a failed call's flags over at the top level in a
      // session; the test kit hands the wrapped { result: … } verbatim —
      // read both (probed 2026-10-05)
      const inner = (r as { isError?: boolean; text?: string; result?: { isError?: boolean; text?: string } }) ?? {}
      const view = inner.result ?? inner
      const failed = inner.isError === true || view.isError === true
      const text = typeof view.text === 'string' ? view.text : ''
      const stats = await read($, stToolStats)
      await update($, stToolStats, (s) => ({
        calls: s.calls + 1,
        failures: s.failures + (failed ? 1 : 0),
      }))
      if (e.tool === 'Bash') {
        const m = /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/pull\/\d+/.exec(text)
        if (m !== null) {
          await post($, gateway, '/v1/bridge/feedback', { session: await sessionOf($), kind: 'pr_created', value: m[0] })
        }
      }
    }
    return r
  })

  on('classic.PostModelSwitch', async ($, e, next) => {
    if (e.source === 'command' || e.source === 'picker') {
      await post($, gateway, '/v1/bridge/feedback', {
        session: await sessionOf($),
        kind: 'manual_model_switch',
        value: `${e.from_model}→${e.to_model}`,
      })
    }
    return next(e)
  })

  on('agent.spawn', async ($, e, next) => {
    // 1. fork: dead branch on CC 2.1.288+ — no fork subagent type exists
    //    (S3); kept for the day one does. Forks inherit the parent model.
    // 2. a model Claude already chose is respected.
    if (e.fork || e.model !== undefined) return next(e)
    // 3. R1: the type settles it
    const fixed = FIXED_AGENT[e.subagentType]
    if (fixed !== undefined) return next({ ...e, model: TIER_ALIAS[fixed] })
    // 4. ask the gateway; failure leaves the spawn unchanged
    const session = await $.session.id()
    const decided = await decideTurn($, gateway, {
      harness: 'claude-code',
      session,
      prompt: e.prompt,
      agent: e.subagentType,
      store_hint: false,
    })
    if (decided === null) return next(e)
    // 5. spawn with the tier's alias; §4.5's env resolves it to the group
    return next({ ...e, model: TIER_ALIAS[decided.tier] })
  })

  on('turn.complete', async ($, e, next) => {
    // SP7 §3.5: the review goes out after a routed main turn that did not
    // end on performance. Fire and forget: the answer is already delivered,
    // and a review that never arrives just means no R3-review next turn.
    if (e.agentId === undefined && !e.isAborted) {
      const turn = await read($, stTurn)
      const prompt = await read($, stPrompt)
      if (turn !== null && turn.tier !== 'performance' && prompt !== null) {
        const stats = await read($, stToolStats)
        void post($, gateway, '/v1/bridge/review', {
          session: await sessionOf($),
          harness: 'claude-code',
          turn_id: e.turnId,
          prompt,
          answer: e.answer,
          tool_calls: stats.calls,
          tool_failures: stats.failures,
        })
      }
    }
    if (e.agentId === undefined) {
      const derived = await read($, stDerived)
      const stored = await read($, stStored)
      if (!derived.is && !stored) {
        await update($, stStored, () => true)
        try {
          const hash = await firstUserHash($)
          if (hash !== null) {
            const session = await sessionOf($)
            const at = await $.clock.now()
            // prune the index to the last 24h, then add this entry ($.store
            // has no enumeration, so the index key is the whole list)
            const idx = ((await $.store.get('qq:first-index')) as Record<string, { session: string; at: number }> | undefined) ?? {}
            const fresh: Record<string, { session: string; at: number }> = {}
            for (const [h, v] of Object.entries(idx)) {
              if (at - v.at < 24 * 3600 * 1000) fresh[h] = v
            }
            fresh[hash] = { session, at }
            await $.store.set('qq:first-index', fresh)
            await $.store.set('qq:first:' + hash, { session, at })
          }
        } catch {
          // storing lineage is best-effort
        }
      }
    }
    return next(e)
  })

  on('turn.step', async function* ($, e, next) {
    // magpie names the group "group/mbridge[1m]" for a 1M window: compare
    // without the "[…]" suffix and keep it on the rewrite (the engine drops
    // it from the request and sends the 1M beta — checked on CC 2.1.296)
    const { base, suffix } = splitSuffix(e.model)
    if (base !== ROUTING_GROUP) return yield* next(e)
    if (e.agentId !== undefined) {
      // an agent that inherited the main model pins to the main tier at
      // first sight and never follows the main session's later moves
      const pinned = (await read($, stAgentTier))[e.agentId]
      if (pinned !== undefined) return yield* next({ ...e, model: tierGroup(pinned) + suffix })
      const main = await read($, stMainTier)
      if (main === null) return yield* next(e) // nothing to pin to yet
      const id = e.agentId
      await update($, stAgentTier, (t) => ({ ...t, [id]: main }))
      return yield* next({ ...e, model: tierGroup(main) + suffix })
    }
    const turn = await read($, stTurn)
    if (turn === null) return yield* next(e) // gateway fallback mode
    return yield* next({ ...e, model: turn.group + suffix })
  })
}

/** The session id, or "" when even that fails. */
async function sessionOf($: any): Promise<string> {
  try {
    return await $.session.id()
  } catch {
    return ''
  }
}

/** fire-and-forget POST to the gateway; swallows every error. */
async function post($: any, gateway: string, path: string, body: unknown): Promise<void> {
  try {
    await $.http.fetch(gateway + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
  } catch {
    // feedback is best-effort
  }
}

/** The /turn call raced against TURN_BUDGET_MS; null on failure/timeout. */
async function decideTurn(
  $: any,
  gateway: string,
  body: Record<string, unknown>,
): Promise<{ tier: Tier; group: string; reason: string } | null> {
  try {
    const res = await Promise.race([
      $.http.fetch(gateway + '/v1/bridge/turn', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
      $.clock.sleep(TURN_BUDGET_MS).then(() => 'timeout' as const),
    ])
    if (res === 'timeout') return null
    if (!res.ok) return null
    const parsed = JSON.parse(res.text) as { tier?: string; group?: string; reason?: string }
    if (typeof parsed.tier !== 'string' || typeof parsed.group !== 'string') return null
    return {
      tier: parsed.tier as Tier,
      group: parsed.group,
      reason: typeof parsed.reason === 'string' ? parsed.reason : '',
    }
  } catch {
    return null
  }
}
