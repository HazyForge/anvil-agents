/** Frontend-only agent card: a browser-assembled run recipe. Not a Kubernetes CR. */
export interface AgentCard {
  id: string;
  title: string;
  description: string;
  namespace: string;
  /** Same-namespace AgentRunProfile name. */
  profileName: string;
  /** Optional harness profile override. */
  harnessProfileName?: string;
  /** Ordered skill-set names composed into the run. */
  skillSetNames: string[];
  /** Ordered tool-set names composed into the run. */
  toolSetNames: string[];
  prompt: string;
  intent?: "observe" | "fixTransient" | "proposeChange" | "cleanup" | "";
  purpose?: "manual" | "adverseSituation" | "scheduledHealthCheck";
  /** Display tags for the card grid. */
  tags: string[];
  createdAt: string;
  updatedAt: string;
  lastRunName?: string;
  lastRunAt?: string;
}

export type AgentCardDraft = Omit<AgentCard, "id" | "createdAt" | "updatedAt" | "lastRunName" | "lastRunAt"> & {
  id?: string;
};

export function emptyCardDraft(namespace: string): AgentCardDraft {
  return {
    title: "",
    description: "",
    namespace,
    profileName: "",
    harnessProfileName: "",
    skillSetNames: [],
    toolSetNames: [],
    prompt: "",
    intent: "observe",
    purpose: "manual",
    tags: [],
  };
}

export function cardSummary(card: AgentCard): string {
  const bits = [
    card.profileName ? `profile:${card.profileName}` : null,
    card.harnessProfileName ? `harness:${card.harnessProfileName}` : null,
    card.skillSetNames.length ? `skills:${card.skillSetNames.length}` : null,
    card.toolSetNames.length ? `tools:${card.toolSetNames.length}` : null,
  ].filter(Boolean);
  return bits.join(" · ") || "incomplete recipe";
}
