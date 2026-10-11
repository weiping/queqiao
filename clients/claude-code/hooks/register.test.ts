import { expect, mock, test } from 'claude-code/testing'

// A fake gateway: every $.http.fetch lands here with its url and body.
function fakeGateway(on: any, turns: Array<{ tier: string; group: string; reason: string }>) {
  const calls: Array<{ url: string; method?: string; body?: string }> = []
  let n = 0
  on('http.fetch', (_$: unknown, e: { url: string; init?: { method?: string; body?: string } }) => {
    // SP10's budget probe is answered, not counted: these tests count /turn and friends
    if (e.url.endsWith('/v1/bridge/router')) return { value: { status: 200, ok: true, headers: {}, text: '{}' } }
    calls.push({ url: e.url, method: e.init?.method, body: e.init?.body })
    if (e.url.endsWith('/v1/bridge/turn')) {
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
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5-classified' }])
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
  expect(calls[0].url).toBe('http://127.0.0.1:3426/v1/bridge/turn')
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
  expect(status).toContain('mbridge: fast · R5-classified')

  // main-loop step on the routing group → this turn's tier group
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()
  expect(seen).toEqual(['group/mb-fast'])
})

// Recorded on Claude Code 2.1.296: with `"model": "group/mbridge[1m]"` (as
// magpie writes it) turn.step's e.model is "group/mbridge[1m]", and a model a
// hook names with "[1m]" goes out without the suffix plus the 1M-context beta.
test('a [1m] routing group is rewritten and keeps its suffix, main and subagent', async ($, on) => {
  fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' }])
  spawnBasics(on)
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string; agentId?: string }) {
    seen.push((e.agentId ?? 'main') + ':' + e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'rename a variable' })
  for (const agentId of [undefined, 'a1', 'a1']) {
    const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge[1m]', messageCount: 1, ...(agentId ? { agentId } : {}) })
    let s = await stream.next()
    while (s.done !== true) s = await stream.next()
  }

  expect(seen).toEqual(['main:group/mb-fast[1m]', 'a1:group/mb-fast[1m]', 'a1:group/mb-fast[1m]'])
})

test('a [1m] model that is not the routing group passes through', async ($, on) => {
  fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' }])
  spawnBasics(on)
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })
  await $.turn.start({ turnId: 't1', text: 'rename a variable' })
  for (const model of ['kimi-code-cn/k3[1m]', 'group/mbridge-old[1m]']) {
    const stream = $.turn.step({ turnId: 't1', index: 0, model, messageCount: 1 })
    let s = await stream.next()
    while (s.done !== true) s = await stream.next()
  }
  expect(seen).toEqual(['kimi-code-cn/k3[1m]', 'group/mbridge-old[1m]'])
})

test('turn.step leaves pinned models and effort alone', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
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
  for (const model of ['group/mb-perf', 'claude-sonnet-5']) {
    const stream = $.turn.step({ turnId: 't1', index: 0, model, messageCount: 1 })
    let step = await stream.next()
    while (step.done !== true) step = await stream.next()
  }
  expect(seen.map((s) => s.model)).toEqual(['group/mb-perf', 'claude-sonnet-5'])
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
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()

  expect(seen).toEqual(['group/mbridge']) // untouched: gateway fallback mode
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
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()

  expect(seen).toEqual(['group/mbridge'])
  expect(status.filter((s) => s.includes('mbridge:')).length).toBe(0)
})

// SP10: mbridge says at session.start how long a turn may take; a /turn
// that answers within it is used even past the old 1500 ms. The fake /turn
// answers when the test releases it, after moving the clock on.
function slowGateway(on: any, routerText: string) {
  let release = () => {}
  on('http.fetch', async (_$: unknown, e: { url: string }) => {
    if (e.url.endsWith('/v1/bridge/router')) return { value: { status: 200, ok: true, headers: {}, text: routerText } }
    if (e.url.endsWith('/v1/bridge/turn')) {
      await new Promise<void>((r) => { release = r })
      return { value: { status: 200, ok: true, headers: {}, text: JSON.stringify({ tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' }) } }
    }
    return { value: { status: 200, ok: true, headers: {}, text: '{}' } }
  })
  return () => release()
}

for (const c of [
  { name: 'a turn_budget_ms of 3000 waits for a /turn that takes 2000 ms', router: '{"turn_budget_ms":3000}', after: 2000, want: 'group/mb-fast' },
  { name: 'without turn_budget_ms the 1500 ms budget still applies', router: '{}', after: 2000, want: 'group/mbridge' },
  { name: 'a turn_budget_ms past 8000 is held to 8000', router: '{"turn_budget_ms":60000}', after: 8100, want: 'group/mbridge' },
  { name: 'a turn_budget_ms under 1500 is held to 1500', router: '{"turn_budget_ms":100}', after: 1000, want: 'group/mb-fast' },
]) {
  test(c.name, async ($, on) => {
    const release = slowGateway(on, c.router)
    on('session.id', () => ({ value: 's-1' }))
    on('ui.status', () => ({ value: undefined }))
    const clock = mock.clock(on)
    on('session.start', () => ({ cwd: '/work/repo' }))
    on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
    const seen: string[] = []
    on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
      seen.push(e.model)
      return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
    })

    await $.session.start({ surface: 'terminal', isInteractive: true, cwd: '/work/repo' })
    const started = $.turn.start({ turnId: 't1', text: 'rename a variable' })
    await clock.advance(c.after)
    release()
    await clock.advance(10000)
    await started
    const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1 })
    let step = await stream.next()
    while (step.done !== true) step = await stream.next()
    expect(seen).toEqual([c.want])
  })
}

// SP10 review: when the session.start probe found mbridge down, or /clear
// reset $.state, the next turn asks for the budget itself.
test('a turn with no budget learned asks mbridge for it before /turn', async ($, on) => {
  const release = slowGateway(on, '{"turn_budget_ms":3000}')
  on('session.id', () => ({ value: 's-1' }))
  on('ui.status', () => ({ value: undefined }))
  const clock = mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  // no session.start: nothing learned yet
  const started = $.turn.start({ turnId: 't1', text: 'rename a variable' })
  await clock.advance(2000)
  release()
  await clock.advance(10000)
  await started
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()
  expect(seen).toEqual(['group/mb-fast'])
})

test('empty-text turns skip /turn and keep the previous tier', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
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
  const stream = $.turn.step({ turnId: 't2', index: 0, model: 'group/mbridge', messageCount: 1 })
  let step = await stream.next()
  while (step.done !== true) step = await stream.next()
  expect(seen).toEqual(['group/mb-fast'])
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
  expect(status).toEqual(['mbridge: 未运行'])
})

// ---- Task 3: subagents ----

function spawnBasics(on: any) {
  on('session.id', () => ({ value: 's-1' }))
  on('ui.status', () => ({ value: undefined }))
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
}

test('agent.spawn: fork and preset models pass through (dead-code branch per S3)', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
  spawnBasics(on)
  const spawns: Array<{ model?: string }> = []
  on('agent.spawn', (_$: unknown, e: { prompt: string; subagentType: string; model?: string; fork?: boolean }) => {
    spawns.push({ model: e.model })
    return { model: e.model ?? 'inherited', agentId: 'a0' }
  })

  await $.agent.spawn({ prompt: 'x', subagentType: 'general-purpose', fork: true })
  await $.agent.spawn({ prompt: 'x', subagentType: 'general-purpose', model: 'claude-sonnet-5' })
  expect(calls.length).toBe(0) // neither reaches the gateway
  expect(spawns).toEqual([{ model: undefined }, { model: 'claude-sonnet-5' }])
})

test('agent.spawn: R1 types map straight to an alias', async ($, on) => {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
  spawnBasics(on)
  const models: Array<string | undefined> = []
  on('agent.spawn', (_$: unknown, e: { prompt: string; subagentType: string; model?: string }) => {
    models.push(e.model)
    return { model: e.model ?? 'inherited', agentId: 'a0' }
  })

  await $.agent.spawn({ prompt: 'find it', subagentType: 'Explore' })
  await $.agent.spawn({ prompt: 'plan it', subagentType: 'Plan' })
  expect(models).toEqual(['haiku', 'opus'])
  expect(calls.length).toBe(0) // R1 needs no /turn call
})

test('agent.spawn: other types ask the gateway with prompt and agent', async ($, on) => {
  const calls = fakeGateway(on, [
    { tier: 'balanced', group: 'group/mb-balanced', reason: 'R5' },
    { tier: 'performance', group: 'group/mb-perf', reason: 'R5' },
  ])
  spawnBasics(on)
  const models: Array<string | undefined> = []
  on('agent.spawn', (_$: unknown, e: { prompt: string; subagentType: string; model?: string }) => {
    models.push(e.model)
    return { model: e.model ?? 'inherited', agentId: 'a0' }
  })

  await $.agent.spawn({ prompt: 'fix the flaky test in rules_test.go', subagentType: 'general-purpose' })
  const body = JSON.parse(calls[0].body ?? '{}')
  expect(calls[0].url).toBe('http://127.0.0.1:3426/v1/bridge/turn')
  expect(body).toMatchObject({ harness: 'claude-code', session: 's-1', agent: 'general-purpose', store_hint: false })
  expect(models).toEqual(['sonnet'])
})

test('agent.spawn: gateway failure leaves the spawn unchanged', async ($, on) => {
  on('http.fetch', () => ({ deny: 'down' }))
  spawnBasics(on)
  const models: Array<string | undefined> = []
  on('agent.spawn', (_$: unknown, e: { prompt: string; subagentType: string; model?: string }) => {
    models.push(e.model)
    return { model: e.model ?? 'inherited', agentId: 'a0' }
  })

  await $.agent.spawn({ prompt: 'do things', subagentType: 'general-purpose' })
  expect(models).toEqual([undefined])
})

test('agentId steps pin at the main tier and stay pinned when the main tier moves', async ($, on) => {
  const calls = fakeGateway(on, [
    { tier: 'fast', group: 'group/mb-fast', reason: 'R5' },
    { tier: 'balanced', group: 'group/mb-balanced', reason: 'R6' },
  ])
  spawnBasics(on)
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string; agentId?: string }) {
    seen.push((e.agentId ?? 'main') + ':' + e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'quick question' }) // → fast
  const step = async (agentId?: string) => {
    const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1, ...(agentId ? { agentId } : {}) })
    let s = await stream.next()
    while (s.done !== true) s = await stream.next()
  }
  await step() // main → mb-fast
  await step('a1') // first sight of a1 → pinned fast
  await $.turn.start({ turnId: 't2', text: 'no wait, refactor everything instead' }) // → balanced
  await step() // main moved to mb-balanced
  await step('a1') // a1 stays pinned

  expect(seen).toEqual(['main:group/mb-fast', 'a1:group/mb-fast', 'main:group/mb-balanced', 'a1:group/mb-fast'])
  void calls
})

test('a subagent that inherits the performance tier goes to group/mb-perf', async ($, on) => {
  fakeGateway(on, [{ tier: 'performance', group: 'group/mb-perf', reason: 'R6' }])
  spawnBasics(on)
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string; agentId?: string }) {
    seen.push((e.agentId ?? 'main') + ':' + e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'refactor everything' }) // → performance
  for (const agentId of [undefined, 'a1', 'a1']) {
    const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1, ...(agentId ? { agentId } : {}) })
    let s = await stream.next()
    while (s.done !== true) s = await stream.next()
  }

  expect(seen).toEqual(['main:group/mb-perf', 'a1:group/mb-perf', 'a1:group/mb-perf'])
})

test('agentId steps pass through when no turn has ever succeeded', async ($, on) => {
  on('http.fetch', () => ({ deny: 'down' }))
  spawnBasics(on)
  const seen: string[] = []
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string; agentId?: string }) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  await $.turn.start({ turnId: 't1', text: 'hi' })
  const stream = $.turn.step({ turnId: 't1', index: 0, model: 'group/mbridge', messageCount: 1, agentId: 'a1' })
  let s = await stream.next()
  while (s.done !== true) s = await stream.next()

  expect(seen).toEqual(['group/mbridge'])
})

// ---- Task 4: feedback ----

// tool.call answers the engine gives back, one per call (default: ok)
function feedbackBasics(on: any, toolAnswers: Array<{ text: string; isError?: boolean }> = []) {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
  const { status } = stubBasics(on)
  mock.clock(on)
  let n = 0
  on('tool.call', () => {
    const a = toolAnswers.length > 0 ? toolAnswers[Math.min(n, toolAnswers.length - 1)] : { text: 'ok' }
    n++
    return { result: { text: a.text, isError: a.isError === true } }
  })
  on('classic.PostModelSwitch', () => ({}))
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.step', async function* (_$: unknown, e: { turnId: string; index: number; model: string }) {
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })
  return { calls, status }
}

test('tool.call counts main-session calls and failures, not subagent ones', async ($, on) => {
  const { calls, status } = feedbackBasics(on, [
    { text: 'ok' },
    { text: 'ls: /nope: No such file or directory', isError: true },
    { text: 'file contents' },
  ])

  await $.tool.call({ tool: 'Bash', command: 'ls', agentId: undefined })
  await $.tool.call({ tool: 'Bash', command: 'ls /nope', agentId: undefined })
  await $.tool.call({ tool: 'Read', file_path: 'x', agentId: 'a1' }) // subagent: not counted

  await $.turn.start({ turnId: 't2', text: 'next turn carries the stats' })
  const body = JSON.parse(calls[calls.length - 1].body ?? '{}')
  expect(body.tool_calls).toBe(2)
  expect(body.tool_failures).toBe(1)
})

test('tool.call stats reset after being reported', async ($, on) => {
  const { calls } = feedbackBasics(on, [{ text: 'ok' }])
  await $.tool.call({ tool: 'Bash', command: 'ls', agentId: undefined })
  await $.turn.start({ turnId: 't1', text: 'one' }) // reports 1/0, resets
  await $.turn.start({ turnId: 't2', text: 'two' }) // reports 0/0
  const bodies = calls.map((c: { body?: string }) => JSON.parse(c.body ?? '{}')).filter((b: any) => b.prompt)
  expect(bodies.map((b: any) => [b.tool_calls, b.tool_failures])).toEqual([
    [1, 0],
    [0, 0],
  ])
})

test('a Bash tool result with a PR link sends pr_created', async ($, on) => {
  const { calls } = feedbackBasics(on, [{ text: 'Opened: https://github.com/weiping/magpie-bridge/pull/6' }])
  await $.tool.call({ tool: 'Bash', command: 'gh pr create', agentId: undefined })
  const fb = calls.find((c: { url: string }) => c.url.endsWith('/v1/bridge/feedback'))
  expect(fb).toBeDefined()
  expect(JSON.parse(fb.body)).toEqual({
    session: 's-1',
    kind: 'pr_created',
    value: 'https://github.com/weiping/magpie-bridge/pull/6',
  })
})

test('no PR link, no feedback event', async ($, on) => {
  const { calls } = feedbackBasics(on, [{ text: 'done' }])
  await $.tool.call({ tool: 'Bash', command: 'ls', agentId: undefined })
  expect(calls.find((c: { url: string }) => c.url.endsWith('/v1/bridge/feedback'))).toBeUndefined()
})

test('classic.PostModelSwitch reports command and picker switches only', async ($, on) => {
  const { calls } = feedbackBasics(on)
  for (const source of ['command', 'picker', 'sdk', 'auto', 'resume']) {
    await $.classic.PostModelSwitch({ from_model: 'group/mbridge', to_model: 'group/mb-perf', source })
  }
  const events = calls
    .filter((c: { url: string }) => c.url.endsWith('/v1/bridge/feedback'))
    .map((c: { body?: string }) => JSON.parse(c.body ?? '{}'))
  expect(events).toEqual([
    { session: 's-1', kind: 'manual_model_switch', value: 'group/mbridge→group/mb-perf' },
    { session: 's-1', kind: 'manual_model_switch', value: 'group/mbridge→group/mb-perf' },
  ])
})

// ---- Task 5: derived sessions (/fork, /branch) ----

function derivedBasics(on: any, opts: { firstUser?: string; derived?: boolean } = {}) {
  const calls = fakeGateway(on, [{ tier: 'fast', group: 'group/mb-fast', reason: 'R5' }])
  stubBasics(on)
  const clock = mock.clock(on)
  const store = new Map<string, unknown>()
  on('store.get', (_$: unknown, e: { key: string }) => ({ value: store.get(e.key) }))
  on('store.set', (_$: unknown, e: { key: string; value: unknown }) => {
    store.set(e.key, e.value)
    return { value: undefined }
  })
  on('session.messages', () => ({
    value: opts.firstUser === undefined ? [] : [{ role: 'user', text: opts.firstUser }],
  }))
  on('classic.SessionStart', () => ({}))
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))
  return { calls, store, clock }
}

test('the first main turn.complete stores the first-message hash; later ones do not', async ($, on) => {
  const { store } = derivedBasics(on, { firstUser: 'help me with the router spec' })

  await $.turn.start({ turnId: 't1', text: 'help me with the router spec' })
  await $.turn.complete({ turnId: 't1', answer: 'ok', durationMs: 10, isAborted: false, usage: null })
  await $.turn.complete({ turnId: 't2', answer: 'ok', durationMs: 10, isAborted: false, usage: null })

  const keys = [...store.keys()]
  expect(keys.filter((k) => k.startsWith('qq:first:')).length).toBe(1)
  const idx = store.get('qq:first-index') as Record<string, { session: string }>
  expect(Object.keys(idx).length).toBe(1)
})

test('a subagent turn.complete does not store anything', async ($, on) => {
  const { store } = derivedBasics(on, { firstUser: 'hello' })
  await $.turn.start({ turnId: 't1', text: 'hello' })
  await $.turn.complete({ turnId: 't1', answer: 'ok', durationMs: 10, isAborted: false, usage: null, agentId: 'a1' })
  expect([...store.keys()].filter((k) => k.startsWith('qq:first'))).toEqual([])
})

test('classic.SessionStart marks fork sessions derived only', async ($, on) => {
  derivedBasics(on)
  for (const source of ['fork', 'startup', 'resume', 'clear']) {
    await $.classic.SessionStart({ source })
  }
  // observable effect: a fork-marked session consults $.store on its first
  // turn (checked in the next test); here we just drive it for coverage
})

test('a derived session sends parent_session from the store on its first turn, once', async ($, on) => {
  const { calls, store } = derivedBasics(on, { firstUser: 'help me with the router spec' })
  // the parent session stored its hash earlier
  const hash = fnv1a('help me with the router spec')
  store.set('qq:first:' + hash, { session: 's-parent', at: 1000 })

  await $.classic.SessionStart({ source: 'fork' })
  await $.turn.start({ turnId: 't1', text: 'help me with the router spec' })
  const first = JSON.parse(calls[0].body ?? '{}')
  expect(first.parent_session).toBe('s-parent')

  // second turn: no further lookup, and the field stays absent without a hit
  await $.turn.start({ turnId: 't2', text: 'and now more' })
  const second = JSON.parse(calls[1].body ?? '{}')
  expect(second.parent_session).toBeUndefined()
})

test('a non-derived session never consults the store', async ($, on) => {
  const { calls, store } = derivedBasics(on, { firstUser: 'plain session' })
  store.set('qq:first:' + fnv1a('plain session'), { session: 'other', at: 1 })
  await $.classic.SessionStart({ source: 'startup' })
  await $.turn.start({ turnId: 't1', text: 'plain session' })
  const first = JSON.parse(calls[0].body ?? '{}')
  expect(first.parent_session).toBeUndefined()
})

test('store entries older than 24h are pruned on write', async ($, on) => {
  const { store, clock } = derivedBasics(on, { firstUser: 'fresh one' })
  const oldHash = fnv1a('an old session opener')
  store.set('qq:first-index', {
    [oldHash]: { session: 's-old', at: 0 },
  })
  store.set('qq:first:' + oldHash, { session: 's-old', at: 0 })

  await clock.set(25 * 3600 * 1000) // 25h later
  await $.turn.start({ turnId: 't1', text: 'fresh one' })
  await $.turn.complete({ turnId: 't1', answer: 'ok', durationMs: 1, isAborted: false, usage: null })

  const idx = store.get('qq:first-index') as Record<string, unknown>
  expect(idx[oldHash]).toBeUndefined()
  expect(idx[fnv1a('fresh one')]).toBeDefined()
})

// the same FNV-1a the mod uses (§5.8: both sides share algorithm and source)
function fnv1a(s: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(16)
}

// ---- SP7: the end-of-turn review ----

// A gateway that answers /turn with `tier` and records /review calls, whose
// response hangs so an awaited one would stall the turn.
function reviewGateway(on: any, tier: string) {
  const reviews: Array<Record<string, unknown>> = []
  on('http.fetch', (_$: unknown, e: { url: string; init?: { body?: string } }) => {
    if (e.url.endsWith('/v1/bridge/review')) {
      reviews.push(JSON.parse(e.init?.body ?? '{}') as Record<string, unknown>)
      return { value: new Promise(() => {}) } // never resolves
    }
    if (e.url.endsWith('/v1/bridge/turn')) {
      return {
        value: { status: 200, ok: true, headers: {}, text: JSON.stringify({ tier, group: 'group/mb-' + tier, reason: 'R5-classified' }) },
      }
    }
    return { value: { status: 200, ok: true, headers: {}, text: '{}' } }
  })
  return reviews
}

test('turn.complete posts a review for a routed main turn and does not wait', async ($, on) => {
  const reviews = reviewGateway(on, 'fast')
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))

  await $.turn.start({ turnId: 't1', text: 'what license is this repo?' })
  const started = Date.now()
  await $.turn.complete({ turnId: 't1', answer: 'ok', durationMs: 10, isAborted: false, usage: null })
  const elapsed = Date.now() - started

  expect(elapsed).toBeLessThan(100) // it did not wait for /review
  expect(reviews.length).toBe(1)
  expect(reviews[0]).toMatchObject({
    session: 's-1',
    harness: 'claude-code',
    turn_id: 't1',
    prompt: 'what license is this repo?',
    answer: 'ok',
    tool_calls: 0,
    tool_failures: 0,
  })
})

test('aborted, performance, subagent and gateway-mode turns post no review', async ($, on) => {
  // one handler, with the behaviour the case needs (the kit forbids
  // registering http.fetch after the test has called $)
  const reviews: Array<Record<string, unknown>> = []
  let mode: 'fast' | 'performance' | 'down' = 'fast'
  on('http.fetch', (_$: unknown, e: { url: string; init?: { body?: string } }) => {
    if (e.url.endsWith('/v1/bridge/review')) {
      reviews.push(JSON.parse(e.init?.body ?? '{}') as Record<string, unknown>)
      return { value: new Promise(() => {}) }
    }
    if (mode === 'down') return { value: { status: 503, ok: false, headers: {}, text: '' } }
    return {
      value: { status: 200, ok: true, headers: {}, text: JSON.stringify({ tier: mode, group: 'group/mb-' + mode, reason: 'R5-classified' }) },
    }
  })
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))

  // aborted
  await $.turn.start({ turnId: 't1', text: 'hello' })
  await $.turn.complete({ turnId: 't1', answer: 'ok', durationMs: 1, isAborted: true, usage: null })
  expect(reviews.length).toBe(0)

  // performance tier
  mode = 'performance'
  await $.turn.start({ turnId: 't2', text: 'hello' })
  await $.turn.complete({ turnId: 't2', answer: 'ok', durationMs: 1, isAborted: false, usage: null })
  expect(reviews.length).toBe(0)

  // subagent
  mode = 'fast'
  await $.turn.start({ turnId: 't3', text: 'hello' })
  await $.turn.complete({ turnId: 't3', answer: 'ok', durationMs: 1, isAborted: false, usage: null, agentId: 'a1' })
  expect(reviews.length).toBe(0)

  // gateway mode: /turn failed, so no turn is recorded
  mode = 'down'
  await $.turn.start({ turnId: 't4', text: 'hello' })
  await $.turn.complete({ turnId: 't4', answer: 'ok', durationMs: 1, isAborted: false, usage: null })
  expect(reviews.length).toBe(0)
})

// SP11: the main session's final answer goes with the next /turn as
// previous_answer, whatever tier the turn was on; a subagent's does not.
test('the next /turn carries the previous main answer', async ($, on) => {
  const calls = fakeGateway(on, [
    { tier: 'performance', group: 'group/mb-perf', reason: 'R6-adopt' },
    { tier: 'performance', group: 'group/mb-perf', reason: 'R6-adopt' },
  ])
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))

  await $.turn.start({ turnId: 't1', text: 'CI 还要吗？' })
  await $.turn.complete({ turnId: 't1', answer: '提议：改三个工作流', durationMs: 10, isAborted: false, usage: null, reason: 'answer' })
  await $.turn.complete({ turnId: 'sub', answer: '子 agent 的回答', durationMs: 10, isAborted: false, usage: null, agentId: 'a1', reason: 'answer' })
  await $.turn.start({ turnId: 't2', text: '按这个思路修改' })

  const turns = calls.filter((c) => c.url.endsWith('/v1/bridge/turn')).map((c) => JSON.parse(c.body ?? '{}'))
  expect(turns.length).toBe(2)
  expect(turns[0].previous_answer).toBeUndefined()
  expect(turns[1].previous_answer).toBe('提议：改三个工作流')
})

// A turn that ends without a full answer (interrupted, an API error) leaves
// no previous answer: the one before it is not what the user now points at.
test('an interrupted or failed turn leaves no previous answer', async ($, on) => {
  const calls = fakeGateway(on, [
    { tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' },
    { tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' },
    { tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' },
    { tier: 'fast', group: 'group/mb-fast', reason: 'R6-adopt' },
  ])
  stubBasics(on)
  mock.clock(on)
  on('turn.start', (_$: unknown, e: { turnId: string }) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))

  await $.turn.start({ turnId: 't1', text: '重构方案？' })
  await $.turn.complete({ turnId: 't1', answer: '方案 A：大重构', durationMs: 10, isAborted: false, usage: null, reason: 'answer' })
  await $.turn.start({ turnId: 't2', text: '先看下 B' })
  await $.turn.complete({ turnId: 't2', answer: 'B 的半截', durationMs: 10, isAborted: true, usage: null, reason: 'aborted' })
  await $.turn.start({ turnId: 't3', text: '按这个思路做' })
  await $.turn.complete({ turnId: 't3', answer: 'API Error: 500', durationMs: 10, isAborted: false, usage: null, reason: 'error' })
  await $.turn.start({ turnId: 't4', text: '继续' })

  const turns = calls.filter((c) => c.url.endsWith('/v1/bridge/turn')).map((c) => JSON.parse(c.body ?? '{}'))
  expect(turns.length).toBe(4)
  expect(turns[1].previous_answer).toBe('方案 A：大重构')
  expect(turns[2].previous_answer).toBeUndefined()
  expect(turns[3].previous_answer).toBeUndefined()
})
