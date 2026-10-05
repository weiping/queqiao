import type { Register } from 'claude-code'

/**
 * queqiao-router: routes each turn across queqiao's tiers.
 *
 * Task 1 scaffold: every hook passes its event through unchanged. The
 * routing behavior lands in Tasks 2–5 per docs/superpowers/plans/
 * 2026-10-05-queqiao-sp3-claude-code.md.
 */
export const register: Register = (on, options) => {
  void options
  on('session.start', async (_$, e, next) => next(e))
  on('classic.SessionStart', async (_$, e, next) => next(e))
  on('classic.UserPromptSubmit', async (_$, e, next) => next(e))
  on('turn.start', async (_$, e, next) => next(e))
  on('turn.step', async function* (_$, e, next) {
    return yield* next(e)
  })
  on('agent.spawn', async (_$, e, next) => next(e))
  on('tool.call', async (_$, e, next) => next(e))
  on('classic.PostModelSwitch', async (_$, e, next) => next(e))
  on('turn.complete', async (_$, e, next) => next(e))
}
