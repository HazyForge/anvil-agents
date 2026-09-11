import type { Prefs, Snapshot } from "./types";

export async function fetchSnapshot(): Promise<Snapshot> {
  const response = await fetch("/local/v1/snapshot");
  if (!response.ok) {
    throw new Error(`snapshot failed: HTTP ${response.status}`);
  }
  return (await response.json()) as Snapshot;
}

export async function savePrefs(prefs: Prefs): Promise<Snapshot> {
  const response = await fetch("/local/v1/prefs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(prefs),
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `save prefs failed: HTTP ${response.status}`);
  }
  return (await response.json()) as Snapshot;
}

export function openConsole(origin: string, path = "/"): void {
  const url = `${origin.replace(/\/$/, "")}${path}`;
  if (window.anvilDesktop?.openConsole) {
    void window.anvilDesktop.openConsole(url);
    return;
  }
  window.open(url, "anvil-agents-console");
}
