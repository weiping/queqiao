import { expect, mock, test } from 'claude-code/testing'

// A fake gateway: every $.http.fetch lands here with its url and body.
function fakeGateway(on: any, turns: Array<{ tier: string; group: string; reason: string }>) {
  const calls: Array<{ url: string; method?: string; body?: string }> = []
  let n = 0
  on('http.fetch', (_$: unknown, e: { url: string; init?: { method?: string; body?: string } }) => {
    calls.push({ url: e.url, method: e.init?.method, body: e.init?.body })
    if (e.url.endsWith('/v1/queqiao/turn')) {
      const t = turns[Math.min(n, turns.length - 1)]
      n++
      return {
        value: { status: 200, ok: true, headers: {}, text: JSON.stringify(t) },
      }
    }
    return { value: { status: 200, ok: true, headers: {}, text: '{}' } }
  })
  return calls
}

function stubBasics(on: any) {
  const status: string[] = []
  on('session.id', () => ({ value: 's-1' }))
  on('ui.status', (_$: unknown, e: { text: string }) => {
    status.push(e.text)
    return { value: undefined }
  })
  return { status }
}

test('turn.start calls /turn with the right fields and turn.step rewrites the routing group', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/qq-fast', reason: 'R5-classified' }])
  const { status } = stubBasics(on)
  const clock = mock.clock(on)
  void clock
  on('classic.UserPromptSubmit', () => ({}))
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  // the engine beneath the mod's turn.step: record what arrives
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.classic.UserPromptSubmit({ prompt: 'what license is this repo?', permission_mode: 'default' })
  await $.turn.start({ turnId: 't1', text: 'what license is this repo?' })

  expect(calls.length).toBe(1)
  expect(calls[0].url).toBe('http://127.0.0.1:3425/v1/queqiao/turn')
  const body = JSON.parse(calls[0].body ?? '{}')
  expect(body).toMatchObject({
    harness: 'claude-code',
    session: 's-1',
    prompt: 'what license is this repo?',
    agent: 'main',
    plan_mode: false,
    tool_calls: 0,
    tool_failures: 0,
    store_hint: false,
  })
  expect(status).toContain('queqiao: fast · R5-classified')

  // main-loop step on the routing group → this turn's tier group
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/queqiao', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()
  expect(seen).toEqual(['group/qq-fast'])
})

test('turn.step leaves pinned models and effort alone', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/qq-fast', reason: 'R5' }])
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  const seen: Array<{ model: string; effort?: string }> = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string; effort?: string }) {
    seen.push({ model: e.model, effort: e.effort })
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'go' })
  // user pinned /model opus; a subagent already chose a tier group
  for (const model of ['group/qq-perf', 'claude-sonnet-5']) {
    const stream = $.turn.step({ turnId: 't1', index: 0, model, messageCount: 1 })
    let step = await stream.next()
    while (step.done !== true) step = await stream.next()
  }
  expect(seen.map((s) => s.model)).toEqual(['group/qq-perf', 'claude-sonnet-5'])
  void calls
})

test('a /turn failure clears the turn and steps pass through', async ($, on) => {
  on('http.fetch', () => ({ deny: 'gateway down' }))
  const { status } = stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'hello' })
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/queqiao', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()

  expect(seen).toEqual(['group/queqiao']) // untouched: gateway fallback mode
  expect(status.filter((s) => s.includes('fast'))).toEqual([])
})

test('the 1500ms timer wins the race when /turn hangs', async ($, on) => {
  let release: (() => void) | null = null
  on('http.fetch', () => ({ value: new Promise(() => {}) }))
  const { status } = stubBasics(on)
  void release
  const clock = mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  const started = $.turn.start({ turnId: 't1', text: 'hello' })
  await clock.advance(1600)
  await started
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/queqiao', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()

  expect(seen).toEqual(['group/queqiao'])
  expect(status.filter((s) => s.includes('queqiao:')).length).toBe(0)
})

test('empty-text turns skip /turn and keep the previous tier', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/qq-fast', reason: 'R5' }])
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'first' })
  await $.turn.start({ turnId: 't2', text: '' }) // continuation turn

  expect(calls.length).toBe(1)
  const stream = $.turn.step({ turnId: 't2', index: 0, model: 'group/queqiao', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()
  expect(seen).toEqual(['group/qq-fast'])
})

test('session.start with the gateway up stays quiet', async ($, on) => {
  const { status } = stubBasics(on)
  mock.clock(on)
  on('session.start', () => ({ cwd: '/work/repo' }))
  on('http.fetch', () => ({ value: { status: 200, ok: true, headers: {}, text: '{}' } }))

  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work/repo' })
  expect(status).toEqual([])
})

test('session.start with the gateway down reports it once', async ($, on) => {
  const { status } = stubBasics(on)
  mock.clock(on)
  on('session.start', () => ({ cwd: '/work/repo' }))
  on('http.fetch', () => ({ deny: 'down' }))

  await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work/repo' })
  expect(status).toEqual(['queqiao: 网关未运行'])
})
