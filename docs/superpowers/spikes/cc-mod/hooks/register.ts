import type { EngineInterface, Register } from 'claude-code'

/**
 * queqiao-spike: the SP0 probe mod.
 *
 * Every hook posts one JSON line to the recorder (fire-and-forget, errors
 * swallowed) and otherwise passes the event on unchanged, except the two
 * rewrites under test:
 *
 *  - turn.step: a `group/queqiao` step is rewritten to main_to on the main
 *    loop and sub_to in an agent loop (agentId set); anything else passes.
 *  - agent.spawn: a spawn without a model and not a fork gets `haiku`.
 */

const MAIN_DEFAULT = 'group/qq-fast'
const SUB_DEFAULT = 'group/qq-balanced'
const ROUTING_GROUP = 'group/queqiao'

type SessionMessageLite = { role: 'user' | 'assistant'; text: string }

function fnv1a(s: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(16)
}

function firstUser(messages: readonly SessionMessageLite[]): string | null {
  for (const m of messages) {
    if (m.role === 'user' && m.text !== '') return m.text
  }
  return null
}

function slowUrlOf(logUrl: string): string {
  return logUrl.replace(/\/spike\/log$/, '/spike/slow?ms=3000')
}

function post($: EngineInterface, logUrl: string, fields: Record<string, unknown>): void {
  $.http
    .fetch(logUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ probe: 'cc', ...fields }),
    })
    .catch(() => {})
}

export const register: Register = (on, options) => {
  const logUrl = String(options.log_url ?? 'http://127.0.0.1:3500/spike/log')
  const mainTo = String(options.main_to ?? MAIN_DEFAULT)
  const subTo = String(options.sub_to ?? SUB_DEFAULT)
  let firstCompleteDone = false

  on('session.start', async ($, e, next) => {
    const session = await $.session.id()
    post($, logUrl, {
      event: 'session.start',
      session,
      isInteractive: e.isInteractive,
      surface: e.surface,
    })
    return next(e)
  })

  on('classic.SessionStart', async ($, e, next) => {
    const session = await $.session.id()
    const messages = await $.session.messages()
    const first = firstUser(messages)
    const hash = first === null ? null : fnv1a(first)
    let storeHit: unknown = null
    if (hash !== null) {
      try {
        storeHit = await $.store.get(`spike:first:${hash}`)
      } catch {
        storeHit = null
      }
    }
    post($, logUrl, {
      event: 'classic.SessionStart',
      session,
      source: e.source,
      seconds_since_last_response: e.seconds_since_last_response ?? null,
      first_head: first === null ? null : first.slice(0, 80),
      first_fnv1a: hash,
      store_lookup: storeHit ?? null,
    })
    return next(e)
  })

  on('classic.UserPromptSubmit', async ($, e, next) => {
    const session = await $.session.id()
    post($, logUrl, {
      event: 'classic.UserPromptSubmit',
      session,
      permission_mode: e.permission_mode ?? null,
      prompt_head: (e.prompt ?? '').slice(0, 40),
    })
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    const session = await $.session.id()
    let winner = 'error'
    try {
      await Promise.race([
        $.http.fetch(slowUrlOf(logUrl)).then(
          () => {
            if (winner === 'error') winner = 'fetch'
          },
          () => {},
        ),
        $.clock.sleep(1500).then(
          () => {
            if (winner === 'error') winner = 'timer'
          },
          () => {},
        ),
      ])
    } catch {
      winner = winner === 'error' ? 'race_threw' : winner
    }
    post($, logUrl, {
      event: 'turn.start',
      session,
      turnId: e.turnId,
      text_head: e.text.slice(0, 40),
      race_winner: winner,
    })
    return next(e)
  })

  on('turn.step', async function* ($, e, next) {
    const session = await $.session.id()
    const to = e.model === ROUTING_GROUP ? (e.agentId !== undefined ? subTo : mainTo) : null
    post($, logUrl, {
      event: 'turn.step',
      session,
      turnId: e.turnId,
      index: e.index,
      model: e.model,
      effort: e.effort ?? null,
      agentId: e.agentId ?? null,
      rewrite_sent: to,
    })
    if (to !== null) {
      yield* next({ ...e, model: to })
      return
    }
    yield* next(e)
  })

  on('agent.spawn', async ($, e, next) => {
    const session = await $.session.id()
    const r = e.fork || e.model !== undefined ? await next(e) : await next({ ...e, model: 'haiku' })
    post($, logUrl, {
      event: 'agent.spawn',
      session,
      fork: e.fork,
      subagentType: e.subagentType,
      model: e.model ?? null,
      parentModel: e.parentModel,
      background: e.background,
      result_model: r.model,
      result_agentId: r.agentId ?? null,
    })
    return r
  })

  on('tool.call', async ($, e, next) => {
    const r = await next(e)
    const session = await $.session.id()
    post($, logUrl, {
      event: 'tool.call',
      session,
      tool: e.tool,
      agentId: e.agentId ?? null,
      isError: r.isError ?? false,
    })
    return r
  })

  on('classic.PostModelSwitch', async ($, e, next) => {
    const session = await $.session.id()
    post($, logUrl, {
      event: 'classic.PostModelSwitch',
      session,
      from_model: e.from_model,
      to_model: e.to_model,
      source: e.source,
    })
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    if (!firstCompleteDone && e.agentId === undefined) {
      firstCompleteDone = true
      const session = await $.session.id()
      const messages = await $.session.messages()
      const first = firstUser(messages)
      const hash = first === null ? null : fnv1a(first)
      if (hash !== null) {
        const at = await $.clock.now()
        try {
          await $.store.set(`spike:first:${hash}`, { session, at })
        } catch {
          // store write failed; still report what we saw
        }
      }
      post($, logUrl, {
        event: 'turn.complete',
        session,
        turnId: e.turnId,
        first_fnv1a: hash,
        stored: hash === null ? null : { session },
      })
    }
    return next(e)
  })
}
