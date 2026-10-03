/**
 * queqiao-spike-pi: the SP0 probe extension (S4, S12).
 *
 * Logs to the recorder (fire-and-forget, errors swallowed) and exercises
 * exactly the two things the spec needs to know:
 *
 *  - S4: does `pi.setModel()` in `before_agent_start` take effect for the
 *    turn's own request, and where does the model object come from;
 *    is there a stable session id.
 *  - S12: does `session_start` with reason "fork" expose a parent session
 *    id; what the Agent tool's parameters look like; does an
 *    `inherit_context` child share the parent's prompt prefix (checked
 *    offline from the recorder's log via first_user_sha256).
 */

import { type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";

const LOG_URL = process.env.QUEQIAO_SPIKE_LOG_URL ?? "http://127.0.0.1:3500/spike/log";
const FAST = "group/qq-fast";
const PROVIDER = "magpie";

function post(fields: Record<string, unknown>): void {
  fetch(LOG_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ probe: "pi", ts: new Date().toISOString(), ...fields }),
  }).catch(() => {});
}

/** Fields of the session header that look like an id or a parent pointer. */
function idFields(header: Record<string, unknown> | null): Record<string, unknown> | null {
  if (header === null) return null;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(header)) {
    if (/id|parent|session/i.test(k)) out[k] = v;
  }
  return out;
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", (event, ctx: ExtensionContext) => {
    const header = ctx.sessionManager.getHeader();
    post({
      event: "session_start",
      reason: event.reason,
      previousSessionFile: event.previousSessionFile ?? null,
      session_id: ctx.sessionManager.getSessionId(),
      session_id_source: "ctx.sessionManager.getSessionId()",
      header_id_fields: idFields(header as unknown as Record<string, unknown> | null),
    });
  });

  pi.on("before_agent_start", async (event, ctx: ExtensionContext) => {
    const model = ctx.modelRegistry.find(PROVIDER, FAST);
    const how = model
      ? `ctx.modelRegistry.find("${PROVIDER}", "${FAST}")`
      : `not found via ctx.modelRegistry.find("${PROVIDER}", "${FAST}")`;
    let ok = false;
    if (model) {
      try {
        ok = await pi.setModel(model);
      } catch {
        ok = false;
      }
    }
    post({
      event: "before_agent_start",
      prompt_head: event.prompt.slice(0, 40),
      set_model_ok: ok,
      model_source: how,
    });
  });

  pi.on("before_provider_request", (event) => {
    const payload = event.payload as { model?: string } | null;
    post({
      event: "before_provider_request",
      body_model: payload?.model ?? null,
    });
  });

  pi.on("before_provider_headers", (event, ctx: ExtensionContext) => {
    const value = `spike-${ctx.sessionManager.getSessionId()}`;
    (event.headers as Record<string, string>)["X-Magpie-Session"] = value;
    post({
      event: "before_provider_headers",
      x_magpie_session: value,
    });
  });

  pi.on("tool_call", (event) => {
    if (event.toolName !== "Agent") return;
    const input = event.input as Record<string, unknown> | undefined;
    if (input && typeof input === "object" && input.inherit_context === true) {
      input.model = `${PROVIDER}/${FAST}`;
    }
    post({
      event: "tool_call",
      toolName: event.toolName,
      input: input ?? null,
    });
  });
}
