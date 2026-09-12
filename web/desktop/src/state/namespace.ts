const NS_KEY = "anvil-agents-desktop.namespace";

export function loadNamespace(fallback = ""): string {
  try {
    return sessionStorage.getItem(NS_KEY)?.trim() || fallback;
  } catch {
    return fallback;
  }
}

export function saveNamespace(namespace: string): void {
  try {
    const trimmed = namespace.trim();
    if (!trimmed) {
      sessionStorage.removeItem(NS_KEY);
      return;
    }
    sessionStorage.setItem(NS_KEY, trimmed);
  } catch {
    // ignore
  }
}
