import { expect, test } from 'claude-code/testing'

// What the SP0 plan's Task 3 Step 1 asks: mock the recorder, drive turn.step
// through the mod, and check the rewrite rule — main steps go to main_to,
// agentId steps to sub_to, and any other model passes through untouched.
test('turn.step rewrites group/queqiao by loop and leaves pinned models alone', async ($, on) => {
  // The recorder's /spike/log endpoint, mocked: every probe post lands here
  on('http.fetch', () => ({ value: { status: 204, ok: true, headers: {}, text: '' } }))
  on('session.id', () => ({ value: 's-test' }))

  // Beneath the mod: the engine's own turn.step, recording what reaches it
  const seen: string[] = []
  on('turn.step', async function* (_$, e) {
    seen.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: 'ok', toolUses: [], stopReason: 'end_turn', usage: null }
  })

  const drive = async (model: string, agentId?: string) => {
    const stream = $.turn.step({
      turnId: 't',
      index: 0,
      model,
      messageCount: 1,
      ...(agentId !== undefined ? { agentId } : {}),
    })
    let step = await stream.next()
    while (step.done !== true) step = await stream.next()
    return step.value
  }

  await drive('group/queqiao') // main-loop step
  await drive('group/queqiao', 'a1') // subagent step
  await drive('group/qq-perf') // model the user pinned with /model

  expect(seen).toEqual(['group/qq-fast', 'group/qq-balanced', 'group/qq-perf'])
})
