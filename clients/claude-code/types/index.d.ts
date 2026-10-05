// The $.state contract of queqiao-router (§6.7 of the design spec).
// plugin.json's "types" points here so claude plugin validate checks that
// register.ts reads and writes these keys, and only these keys.
export interface PluginState {
  'queqiao-router': {
    /** This turn's decision; null when /turn failed or hasn't run. */
    turn: { turnId: string; tier: Tier; group: string } | null
    /** The main session's latest tier, for pinning derived agents. */
    mainTier: Tier | null
    /** agentId → tier, pinned the first time the agent is seen. */
    agentTier: Record<string, Tier>
    /** This turn's main-session tool stats; reported with the next /turn. */
    toolStats: { calls: number; failures: number }
    /** Whether this session came from /fork or /branch, and whether the
     *  parent has been looked up in $.store yet. */
    derived: { is: boolean; checked: boolean }
    /** The last permission mode seen (plan detection), from
     *  classic.UserPromptSubmit. */
    planMode: boolean
    /** The session's cwd, from classic.SessionStart, when it fires. */
    cwd: string | null
  }
}

export type Tier = 'fast' | 'balanced' | 'performance'
