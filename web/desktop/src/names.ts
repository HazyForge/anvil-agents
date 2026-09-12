/** Human-readable speaker label for a DNS-1123 AgentRunProfile name. */
export function personaLabel(name: string): string {
  const value = name.trim();
  if (!value || value === "user") {
    return "You";
  }
  if (value === "wrapper" || value === "anvil-desktop-wrapper") {
    return "Wrapper";
  }
  return value
    .split(/[-_]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}
