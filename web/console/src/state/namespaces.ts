const NAMESPACES_KEY = "anvil-agents-console.namespaces";
const ACTIVE_KEY = "anvil-agents-console.activeNamespace";

const DEFAULT_NAMESPACES = ["hazy-trade"];

export function loadNamespaces(): string[] {
  try {
    const raw = localStorage.getItem(NAMESPACES_KEY);
    if (!raw) {
      return [...DEFAULT_NAMESPACES];
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      return [...DEFAULT_NAMESPACES];
    }
    const cleaned = uniqueNamespaces(parsed.map(String));
    return cleaned.length > 0 ? cleaned : [...DEFAULT_NAMESPACES];
  } catch {
    return [...DEFAULT_NAMESPACES];
  }
}

export function saveNamespaces(namespaces: string[]): void {
  try {
    localStorage.setItem(NAMESPACES_KEY, JSON.stringify(uniqueNamespaces(namespaces)));
  } catch {
    // ignore quota / private mode
  }
}

export function loadActiveNamespace(namespaces: string[]): string {
  try {
    const active = localStorage.getItem(ACTIVE_KEY)?.trim() ?? "";
    if (active && namespaces.includes(active)) {
      return active;
    }
  } catch {
    // ignore
  }
  return namespaces[0] ?? "";
}

export function saveActiveNamespace(namespace: string): void {
  try {
    if (!namespace.trim()) {
      localStorage.removeItem(ACTIVE_KEY);
      return;
    }
    localStorage.setItem(ACTIVE_KEY, namespace.trim());
  } catch {
    // ignore
  }
}

export function uniqueNamespaces(values: string[]): string[] {
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (trimmed && !out.includes(trimmed)) {
      out.push(trimmed);
    }
  }
  return out;
}
