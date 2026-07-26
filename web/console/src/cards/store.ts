import type { AgentCard, AgentCardDraft } from "./types";

const STORAGE_KEY = "anvil-agents.console.cards.v1";

function nowISO(): string {
  return new Date().toISOString();
}

function randomId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `card-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function normalizeList(values: unknown): string[] {
  if (!Array.isArray(values)) {
    return [];
  }
  return values
    .map((value) => String(value ?? "").trim())
    .filter(Boolean);
}

function normalizeCard(raw: unknown): AgentCard | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const value = raw as Record<string, unknown>;
  const id = String(value.id ?? "").trim();
  const title = String(value.title ?? "").trim();
  const namespace = String(value.namespace ?? "").trim();
  const profileName = String(value.profileName ?? "").trim();
  const prompt = String(value.prompt ?? "");
  if (!id || !title || !namespace || !profileName) {
    return null;
  }
  return {
    id,
    title,
    description: String(value.description ?? ""),
    namespace,
    profileName,
    harnessProfileName: String(value.harnessProfileName ?? "").trim() || undefined,
    skillSetNames: normalizeList(value.skillSetNames),
    toolSetNames: normalizeList(value.toolSetNames),
    prompt,
    intent: (String(value.intent ?? "") as AgentCard["intent"]) || "",
    purpose: (String(value.purpose ?? "manual") as AgentCard["purpose"]) || "manual",
    tags: normalizeList(value.tags),
    createdAt: String(value.createdAt ?? nowISO()),
    updatedAt: String(value.updatedAt ?? nowISO()),
    lastRunName: value.lastRunName ? String(value.lastRunName) : undefined,
    lastRunAt: value.lastRunAt ? String(value.lastRunAt) : undefined,
  };
}

export function loadCards(): AgentCard[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed
      .map(normalizeCard)
      .filter((card): card is AgentCard => Boolean(card))
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  } catch {
    return [];
  }
}

function saveAll(cards: AgentCard[]): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(cards));
}

export function listCards(namespace?: string): AgentCard[] {
  const cards = loadCards();
  if (!namespace) {
    return cards;
  }
  return cards.filter((card) => card.namespace === namespace);
}

export function getCard(id: string): AgentCard | null {
  return loadCards().find((card) => card.id === id) ?? null;
}

export function upsertCard(draft: AgentCardDraft): AgentCard {
  const cards = loadCards();
  const existing = draft.id ? cards.find((card) => card.id === draft.id) : undefined;
  const timestamp = nowISO();
  const next: AgentCard = {
    id: existing?.id ?? randomId(),
    title: draft.title.trim(),
    description: draft.description.trim(),
    namespace: draft.namespace.trim(),
    profileName: draft.profileName.trim(),
    harnessProfileName: draft.harnessProfileName?.trim() || undefined,
    skillSetNames: normalizeList(draft.skillSetNames),
    toolSetNames: normalizeList(draft.toolSetNames),
    prompt: draft.prompt,
    intent: draft.intent || "",
    purpose: draft.purpose || "manual",
    tags: normalizeList(draft.tags),
    createdAt: existing?.createdAt ?? timestamp,
    updatedAt: timestamp,
    lastRunName: existing?.lastRunName,
    lastRunAt: existing?.lastRunAt,
  };
  if (!next.title) {
    throw new Error("Card title is required");
  }
  if (!next.namespace) {
    throw new Error("Namespace is required");
  }
  if (!next.profileName) {
    throw new Error("Profile name is required");
  }
  if (!next.prompt.trim()) {
    throw new Error("Prompt is required");
  }
  const without = cards.filter((card) => card.id !== next.id);
  saveAll([next, ...without]);
  return next;
}

export function deleteCard(id: string): void {
  saveAll(loadCards().filter((card) => card.id !== id));
}

export function recordCardRun(id: string, runName: string): AgentCard | null {
  const cards = loadCards();
  const index = cards.findIndex((card) => card.id === id);
  if (index < 0) {
    return null;
  }
  const updated: AgentCard = {
    ...cards[index],
    lastRunName: runName,
    lastRunAt: nowISO(),
    updatedAt: nowISO(),
  };
  const next = [...cards];
  next[index] = updated;
  saveAll(next);
  return updated;
}

export function exportCardsJSON(namespace?: string): string {
  return JSON.stringify(listCards(namespace), null, 2);
}

export function importCardsJSON(raw: string, defaultNamespace?: string): number {
  const parsed = JSON.parse(raw) as unknown;
  const items = Array.isArray(parsed) ? parsed : [parsed];
  let count = 0;
  for (const item of items) {
    const card = normalizeCard(item);
    if (!card) {
      continue;
    }
    upsertCard({
      ...card,
      namespace: card.namespace || defaultNamespace || "",
      id: undefined, // import as new cards
    });
    count += 1;
  }
  return count;
}
