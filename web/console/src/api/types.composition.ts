export type CompositionPathSegment =
  | "agent-run-profiles"
  | "agent-harness-profiles"
  | "agent-skill-sets"
  | "agent-tool-sets"
  | "volume-profiles"
  | "agent-data-volumes"
  | "agent-auth-sessions";

export type CompositionKindName =
  | "AgentRunProfile"
  | "AgentHarnessProfile"
  | "AgentSkillSet"
  | "AgentToolSet"
  | "VolumeProfile"
  | "AgentDataVolume"
  | "AgentAuthSession";

export type CompositionManagementReason =
  | "console_managed"
  | "gitops_protected"
  | "not_console_managed"
  | string;

export interface CompositionManagement {
  writable: boolean;
  reason: CompositionManagementReason;
  managedBy?: string;
}

export interface CompositionMetadata {
  name: string;
  namespace: string;
  uid?: string;
  resourceVersion?: string;
  generation?: number;
  creationTimestamp?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface CompositionDocument {
  apiVersion: string;
  kind: CompositionKindName;
  metadata: CompositionMetadata;
  /** Full CRD spec object (shape depends on kind). */
  spec: Record<string, unknown>;
  status?: Record<string, unknown>;
  management: CompositionManagement;
}

export interface CompositionListResponse {
  items: CompositionDocument[];
}

export interface CompositionKindInfo {
  segment: CompositionPathSegment;
  kind: CompositionKindName;
  title: string;
  plural: string;
  route: string;
  description: string;
  danger?: boolean;
  /** Append-only CRs: browse as cards; create via operator CLI, not console write. */
  appendOnly?: boolean;
}

export const COMPOSITION_KINDS: CompositionKindInfo[] = [
  {
    segment: "agent-run-profiles",
    kind: "AgentRunProfile",
    title: "Run profiles",
    plural: "profiles",
    route: "profiles",
    description: "Role, scope, intent, and skill/tool composition defaults",
  },
  {
    segment: "agent-harness-profiles",
    kind: "AgentHarnessProfile",
    title: "Harness profiles",
    plural: "harness profiles",
    route: "harness-profiles",
    description:
      "Runtime machine for AgentRuns: pick backend (Codex/OpenCode/Grok/…), identity, secrets, volumes, resources",
    danger: true,
  },
  {
    segment: "agent-skill-sets",
    kind: "AgentSkillSet",
    title: "Skill sets",
    plural: "skill sets",
    route: "skill-sets",
    description: "Backend-neutral instruction packs and personas",
  },
  {
    segment: "agent-tool-sets",
    kind: "AgentToolSet",
    title: "Tool sets",
    plural: "tool sets",
    route: "tool-sets",
    description: "Setup scripts and verify contracts (code-execution authority)",
    danger: true,
  },
  {
    segment: "volume-profiles",
    kind: "VolumeProfile",
    title: "Volume profiles",
    plural: "volume profiles",
    route: "volume-profiles",
    description: "Reusable durable storage shapes",
  },
  {
    segment: "agent-data-volumes",
    kind: "AgentDataVolume",
    title: "Data volumes",
    plural: "data volumes",
    route: "data-volumes",
    description: "Concrete agent homes and PVC ownership — durable auth targets",
  },
  {
    segment: "agent-auth-sessions",
    kind: "AgentAuthSession",
    title: "Auth sessions",
    plural: "auth sessions",
    route: "auth-sessions",
    description:
      "Append-only reauth/logout maintenance on data volumes (blocks AgentRuns while active)",
    danger: true,
    appendOnly: true,
  },
];

export function compositionKindByRoute(route: string): CompositionKindInfo | undefined {
  return COMPOSITION_KINDS.find((item) => item.route === route);
}

export function compositionKindBySegment(segment: string): CompositionKindInfo | undefined {
  return COMPOSITION_KINDS.find((item) => item.segment === segment);
}
