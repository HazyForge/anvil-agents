export type CompositionPathSegment =
  | "agent-run-profiles"
  | "agent-harness-profiles"
  | "agent-skills"
  | "agent-tools"
  | "agent-mcp-servers"
  | "agent-mcp-sets"
  | "agent-skill-sets"
  | "agent-tool-sets"
  | "volume-profiles"
  | "agent-data-volumes";

export type CompositionKindName =
  | "AgentRunProfile"
  | "AgentHarnessProfile"
  | "AgentSkill"
  | "AgentTool"
  | "AgentMCPServer"
  | "AgentMCPSet"
  | "AgentSkillSet"
  | "AgentToolSet"
  | "VolumeProfile"
  | "AgentDataVolume";

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
  category?: "atomic" | "collection" | "runtime";
  danger?: boolean;
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
    category: "runtime",
    danger: true,
  },
  {
    segment: "agent-skills",
    kind: "AgentSkill",
    title: "Skills",
    plural: "skills",
    route: "skills",
    description: "One Markdown-only instruction package",
    category: "atomic",
  },
  {
    segment: "agent-tools",
    kind: "AgentTool",
    title: "Tools",
    plural: "tools",
    route: "tools",
    description: "One runtime-acquired executable or script",
    category: "atomic",
    danger: true,
  },
  {
    segment: "agent-mcp-servers",
    kind: "AgentMCPServer",
    title: "MCP servers",
    plural: "MCP servers",
    route: "mcp-servers",
    description: "One secret-free stdio or Streamable HTTP MCP connection",
    category: "atomic",
  },
  {
    segment: "agent-mcp-sets",
    kind: "AgentMCPSet",
    title: "MCP sets",
    plural: "MCP sets",
    route: "mcp-sets",
    description: "Ordered collections of MCP servers",
    category: "collection",
  },
  {
    segment: "agent-skill-sets",
    kind: "AgentSkillSet",
    title: "Skill sets",
    plural: "skill sets",
    route: "skill-sets",
    description: "Ordered collections of atomic skills",
    category: "collection",
  },
  {
    segment: "agent-tool-sets",
    kind: "AgentToolSet",
    title: "Tool sets",
    plural: "tool sets",
    route: "tool-sets",
    description: "Ordered collections of atomic tools (code-execution authority)",
    category: "collection",
    danger: true,
  },
  {
    segment: "volume-profiles",
    kind: "VolumeProfile",
    title: "Volume profiles",
    plural: "volume profiles",
    route: "volume-profiles",
    description: "Reusable durable storage shapes",
    category: "runtime",
  },
  {
    segment: "agent-data-volumes",
    kind: "AgentDataVolume",
    title: "Data volumes",
    plural: "data volumes",
    route: "data-volumes",
    description: "Concrete agent homes and PVC ownership — durable auth targets",
    category: "runtime",
  },
];

export function compositionKindByRoute(route: string): CompositionKindInfo | undefined {
  return COMPOSITION_KINDS.find((item) => item.route === route);
}

export function compositionKindBySegment(segment: string): CompositionKindInfo | undefined {
  return COMPOSITION_KINDS.find((item) => item.segment === segment);
}
