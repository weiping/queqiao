import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent"
import { QueqiaoClient } from "../src/client.js"
import { idsFrom } from "../src/session.js"

/**
 * pi-queqiao: routes each Pi turn across queqiao's tiers (§6.8).
 *
 * before_agent_start asks the gateway which tier this prompt deserves and
 * switches Pi's model when the tier moved; every failure keeps the model
 * as-is, so the worst outcome is "the default tier served this turn".
 */

const PROVIDER = "magpie"
const PR_LINK = /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/pull\/\d+/
const SUBAGENT_TOOLS = new Set(["subagent", "dispatch_agent"]) // S12

type AgentMessageLite = { role?: string; content?: unknown }

// lastAssistantText is S14's reading of the turn's final assistant text:
// a plain string, or the text parts of a content array joined.
function lastAssistantText(messages: AgentMessageLite[]): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m?.role !== "assistant") continue
    if (typeof m.content === "string") return m.content
    if (Array.isArray(m.content)) {
      const parts = m.content as Array<{ type?: string; text?: string }>
      const text = parts
        .filter((p) => p?.type === "text" && typeof p.text === "string")
        .map((p) => p.text as string)
        .join("")
      return text === "" ? null : text
    }
    return null
  }
  return null
}

export default function (pi: ExtensionAPI): void {
  const client = new QueqiaoClient(process.env.QUEQIAO_URL ?? "http://127.0.0.1:3425")

  // per-session memory; rebuilt at every session_start
  let session = ""
  let parent: string | null = null
  let parentSent = false
  let lastTier: string | null = null
  let lastPrompt = "" // this turn's words, for the end-of-turn review (SP7)
  let lastAutoModel = "" // our own setModel, told apart from a manual switch
  let manualPinned = false

  pi.on("session_start", (event, ctx: ExtensionContext) => {
    const ids = idsFrom(
      () => ctx.sessionManager.getSessionId(),
      ctx.sessionManager.getHeader() as Record<string, unknown> | null,
    )
    session = ids.id
    parent = ids.parent
    // S12: reason stays "startup"; when no parent pointer shows up but Pi
    // reports a fork, mark the lineage and let the gateway match by words
    if (parent === null && (event as { reason?: string }).reason === "fork") {
      void client.lineage({ session, source: "pi-fork" })
    }
    parentSent = false
    lastTier = null
    lastPrompt = ""
    lastAutoModel = ""
    manualPinned = false
  })

  pi.on("before_provider_headers", (event) => {
    // §6.8: the gateway reads this to stat the session's tools and spacing
    event.headers["X-Magpie-Session"] = session
  })

  pi.on("before_agent_start", async (event, ctx: ExtensionContext) => {
    const prompt = event.prompt ?? ""
    if (prompt === "" || session === "" || manualPinned) return
    lastPrompt = prompt
    const out = await client.turn({
      session,
      prompt,
      cwd: process.cwd(), // §4.1 project-level criteria
      parentSession: parent !== null && !parentSent ? parent : undefined,
    })
    if (parent !== null) parentSent = true
    if (out === null) return
    if (out.tier === lastTier) return
    const model = ctx.modelRegistry.find(PROVIDER, out.group)
    if (model === undefined) return // tier group unknown to Pi: keep the model
    lastAutoModel = model.id
    lastTier = out.tier
    await pi.setModel(model)
  })

  // SP7 §3.5: after a routed main turn that did not run on performance,
  // ask the gateway to judge whether the answer left the request open.
  // Fire and forget: nothing here waits on the gateway.
  pi.on("agent_end", (event) => {
    if (manualPinned || session === "" || lastTier === null || lastTier === "performance") return
    const answer = lastAssistantText((event as { messages?: AgentMessageLite[] }).messages ?? [])
    if (answer === null) return
    client.review({ session, prompt: lastPrompt, answer })
  })

  pi.on("model_select", (event) => {
    const e = event as { source: string; model: { id: string }; previousModel?: { id: string } }
    if (e.source === "restore") return // a resumed session reloading its model
    if (e.model?.id === lastAutoModel) return // our own switch
    manualPinned = true
    void client.feedback({
      session,
      kind: "manual_model_switch",
      value: `${e.previousModel?.id ?? ""}→${e.model?.id ?? ""}`,
    })
  })

  pi.on("tool_result", (event) => {
    const e = event as { isError?: boolean; content?: Array<{ type?: string; text?: string }> }
    if (e.isError === true) return
    for (const part of e.content ?? []) {
      if (typeof part.text !== "string") continue
      const m = PR_LINK.exec(part.text)
      if (m !== null) {
        void client.feedback({ session, kind: "pr_created", value: m[0] })
        return
      }
    }
  })

  pi.on("tool_call", async (event) => {
    const e = event as { toolName: string; input: Record<string, unknown> }
    if (!SUBAGENT_TOOLS.has(e.toolName)) return
    if (typeof e.input.model === "string") return // already chosen
    if (e.input.context === "fork") return // inherits the parent's context: gateway mode (S12)
    if (manualPinned || session === "") return
    const task = [e.input.task, e.input.prompt, e.input.description].find(
      (v): v is string => typeof v === "string" && v !== "",
    )
    if (task === undefined) return
    const out = await client.turn({ session, prompt: task, agent: "subagent", cwd: process.cwd() })
    if (out === null) return
    // out.group is the tier's real group id (qq-perf, not qq-performance)
    e.input.model = `${PROVIDER}/${out.group}`
  })
}
