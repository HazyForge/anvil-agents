/** Parse spawn/talk intent from a wrapper chat line. DNS-1123 labels only. */

const COUNT_WORDS: Record<string, number> = {
  a: 1,
  an: 1,
  one: 1,
  two: 2,
  three: 3,
  four: 4,
  five: 5,
};

const DEFAULT_NAMES = ["scout", "cartographer", "auditor", "implementer", "navigator"];

export const WRAPPER_PROFILE_NAME = "anvil-desktop-wrapper";

export type WrapperIntent = {
  spawn: boolean;
  talk: boolean;
  names: string[];
};

export function dnsLabel(raw: string): string {
  let value = raw.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/-+/g, "-");
  value = value.replace(/^-+/, "").replace(/-+$/, "");
  if (value.length > 63) {
    value = value.slice(0, 63).replace(/-+$/, "");
  }
  if (!value || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(value)) {
    return "";
  }
  return value;
}

function splitNames(raw: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const part of raw.split(/\s*(?:,|&|and)\s*/i)) {
    const name = dnsLabel(part);
    if (!name || seen.has(name) || name === WRAPPER_PROFILE_NAME) {
      continue;
    }
    seen.add(name);
    out.push(name);
  }
  return out;
}

function countFromText(text: string): number {
  const match = text.match(
    /\b(?:create|spawn|make|add)\s+(\d+|a|an|one|two|three|four|five)\s+(?:new\s+)?(?:agents?|profiles?|entities|entity)\b/i,
  );
  if (!match) {
    return 0;
  }
  const raw = match[1].toLowerCase();
  if (/^\d+$/.test(raw)) {
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? Math.min(5, Math.max(1, n)) : 0;
  }
  return COUNT_WORDS[raw] ?? 0;
}

function namesFromText(text: string): string[] {
  const named = text.match(/\bnamed\s+(.+?)(?:[.!?,]|$)/i);
  if (named) {
    return splitNames(named[1]);
  }
  const called = text.match(/\bcalled\s+([a-zA-Z0-9-]+)/i);
  if (called) {
    const name = dnsLabel(called[1]);
    return name ? [name] : [];
  }
  const colon = text.match(/\b(?:agents?|profiles?|entities)\s*:\s*([a-zA-Z0-9, \-]+)/i);
  if (colon) {
    return splitNames(colon[1]);
  }
  return [];
}

export function parseWrapperIntent(text: string, existing: string[] = []): WrapperIntent {
  const talk = /\b(talk|chat|message|greet|introduce|hello|each other|peer)\b/i.test(text);
  const spawn = /\b(create|spawn|make|add)\b/i.test(text) && /\b(agent|profile|entity|entities|agents|profiles)\b/i.test(text);
  const names = namesFromText(text);
  if (names.length > 0) {
    return { spawn: spawn || names.length > 0, talk, names };
  }
  if (!spawn) {
    return { spawn: false, talk, names: [] };
  }
  const count = countFromText(text) || 2;
  const used = new Set(existing.map((name) => name.trim()).filter(Boolean));
  const generated: string[] = [];
  for (const candidate of DEFAULT_NAMES) {
    if (generated.length >= count) {
      break;
    }
    if (!used.has(candidate)) {
      generated.push(candidate);
      used.add(candidate);
    }
  }
  let i = 1;
  while (generated.length < count) {
    const candidate = `desktop-entity-${i}`;
    i += 1;
    if (used.has(candidate)) {
      continue;
    }
    generated.push(candidate);
    used.add(candidate);
  }
  return { spawn: true, talk, names: generated };
}
