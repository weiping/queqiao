import { atom, read, update, type Register } from 'claude-code'

/**
 * queqiao-router: routes each Claude Code turn across queqiao's tiers
 * (fast / balanced / performance) by asking the queqiao gateway at
 * turn.start and rewriting the model at turn.step. Fail-safe throughout:
 * the worst outcome of any failure is a request left as group/queqiao,
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

const stTurn = atom({ plugin: 'queqiao-router', key: 'turn' }, null as Turn | null)
const stMainTier = atom({ plugin: 'queqiao-router', key: 'mainTier' }, null as Tier | null)
const stAgentTier = atom({ plugin: 'queqiao-router', key: 'agentTier' }, {} as Record<string, Tier>)
const stToolStats = atom({ plugin: 'queqiao-router', key: 'toolStats' }, { calls: 0, failures: 0 })
const stDerived = atom({ plugin: 'queqiao-router', key: 'derived' }, { is: false, checked: false })
const stPlanMode = atom({ plugin: 'queqiao-router', key: 'planMode' }, false)
const stCwd = atom({ plugin: 'queqiao-router', key: 'cwd' }, null as string | null)

const ROUTING_GROUP = 'group/queqiao'
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
const tierGroup = (tier: Tier): string => 'group/qq-' + tier

export const register: Register = (on, options) => {
  const gateway = String(options.gateway_url ?? 'http://127.0.0.1:3425').replace(/\/$/, '')

  on('session.start', async ($, e, next) => {
    if (typeof e.cwd === 'string' && e.cwd !== '') await update($, stCwd, () => e.cwd)
    // one reachability probe; being down changes nothing else
    try {
      const res = await $.http.fetch(gateway + '/v1/queqiao/router', { method: 'GET' })
      if (!res.ok) throw new Error(String(res.status))
    } catch {
      try {
        await $.ui.status('queqiao: 网关未运行')
      } catch {
        // ui.status refused; nothing more to do
      }
    }
    return next(e)
  })

  // plan detection (S1's proven read); empty-text turns keep the last value
  on('classic.UserPromptSubmit', async ($, e, next) => {
    await update($, stPlanMode, () => e.permission_mode === 'plan')
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (e.text !== '') {
      const session = await $.session.id()
      const stats = await read($, stToolStats)
      const planMode = await read($, stPlanMode)
      const cwd = await read($, stCwd)
      const decided = await decideTurn($, gateway, {
        harness: 'claude-code',
        session,
        prompt: e.text,
        agent: 'main',
        plan_mode: planMode,
        ...(cwd !== null ? { cwd } : {}),
        tool_calls: stats.calls,
        tool_failures: stats.failures,
        store_hint: false,
      })
      if (decided !== null) {
        await update($, stTurn, () => ({ turnId: e.turnId, tier: decided.tier, group: decided.group }))
        await update($, stMainTier, () => decided.tier)
        try {
          await $.ui.status(`queqiao: ${decided.tier} · ${decided.reason}`)
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

  on('turn.step', async function* ($, e, next) {
    if (e.model !== ROUTING_GROUP) return yield* next(e)
    if (e.agentId !== undefined) {
      // an agent that inherited the main model pins to the main tier at
      // first sight and never follows the main session's later moves
      const pinned = (await read($, stAgentTier))[e.agentId]
      if (pinned !== undefined) return yield* next({ ...e, model: tierGroup(pinned) })
      const main = await read($, stMainTier)
      if (main === null) return yield* next(e) // nothing to pin to yet
      const id = e.agentId
      await update($, stAgentTier, (t) => ({ ...t, [id]: main }))
      return yield* next({ ...e, model: tierGroup(main) })
    }
    const turn = await read($, stTurn)
    if (turn === null) return yield* next(e) // gateway fallback mode
    return yield* next({ ...e, model: turn.group })
  })
}

/** The /turn call raced against TURN_BUDGET_MS; null on failure/timeout. */
async function decideTurn(
  $: any,
  gateway: string,
  body: Record<string, unknown>,
): Promise<{ tier: Tier; group: string; reason: string } | null> {
  try {
    const res = await Promise.race([
      $.http.fetch(gateway + '/v1/queqiao/turn', {
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
