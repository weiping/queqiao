/**
 * The session id and the parent of a derived session (§5.8, S12):
 * Pi's session header exposes parentSession as the parent's session FILE
 * path (…/sessions/<id>.jsonl); the id is the basename without .jsonl.
 */

export interface SessionIds {
  id: string
  /** The parent session id, when this session was forked from one. */
  parent: string | null
}

/** uuid fallback when Pi provides no stable id (S4 always provided one). */
export function randomId(): string {
  return globalThis.crypto?.randomUUID?.() ?? `qq-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function parentIdFromHeader(header: Record<string, unknown> | null | undefined): string | null {
  const v = header?.parentSession
  if (typeof v !== "string" || v === "") return null
  const base = v.split("/").pop() ?? ""
  const id = base.endsWith(".jsonl") ? base.slice(0, -".jsonl".length) : base
  return id === "" ? null : id
}

export function idsFrom(getSessionId: () => string | undefined, header: Record<string, unknown> | null | undefined): SessionIds {
  const raw = getSessionId()
  return { id: typeof raw === "string" && raw !== "" ? raw : randomId(), parent: parentIdFromHeader(header) }
}
