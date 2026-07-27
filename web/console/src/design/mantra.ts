/**
 * Console design mantra for Anvil Agents.
 *
 * Every first-class cluster object the operator browses is a Kubernetes CRD
 * (AgentRun, AgentRunProfile, AgentHarnessProfile, skill/tool sets, volumes…).
 * Browse and choose those objects as **cards**, not spreadsheets or raw YAML.
 *
 * - List surfaces: card grids with identity (icon/name), short description,
 *   management/state chips, and one primary action.
 * - Compose/create: pick related CRDs as cards (or guided fields that map to
 *   a CRD), not free-form identity dumps.
 * - Raw JSON/YAML is an advanced escape hatch, never the default browse UX.
 */
export const CRD_AS_CARD_MANTRA =
  "If it is a CRD, present it as a card.";

export const CRD_AS_CARD_HELP =
  "Browse composition and run objects as cards — identity, status, and one click to open. Tables and raw JSON stay advanced-only.";
