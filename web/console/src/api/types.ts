export type AgentRunPhase =
  | "Pending"
  | "Running"
  | "Succeeded"
  | "Failed"
  | "NeedsHuman"
  | string;

export interface AgentRunSourceView {
  apiVersion?: string;
  kind?: string;
  namespace?: string;
  name?: string;
}

export interface NamespacedObjectReference {
  name?: string;
  namespace?: string;
}

export interface Condition {
  type?: string;
  status?: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
  observedGeneration?: number;
}

export interface AgentRunDecision {
  classification?: string;
  action?: string;
  summary?: string;
  residualRisk?: string;
}

export interface AgentRunReport {
  type?: string;
  observedAt?: string;
  level?: string;
  stage?: string;
  classification?: string;
  action?: string;
  summary?: string;
  detail?: string;
  pullRequestURL?: string;
  residualRisk?: string;
  needsHuman?: boolean;
  humanFollowUp?: string;
}

export interface ResolvedObjectRef {
  name: string;
  namespace?: string;
  uid?: string;
  generation?: number;
  resourceVersion?: string;
  digest?: string;
}

export interface ResolvedComposition {
  resolvedAt?: string;
  profileRef?: ResolvedObjectRef;
  harnessProfileRef?: ResolvedObjectRef;
  skillSetRefs?: ResolvedObjectRef[];
  toolSetRefs?: ResolvedObjectRef[];
  scope?: {
    application?: string;
    applicationTarget?: string;
  };
  effectiveDigest?: string;
  payloadDigest?: string;
}

export interface AgentRunView {
  name: string;
  namespace: string;
  uid: string;
  resourceVersion: string;
  createdAt: string;
  phase?: AgentRunPhase;
  backend?: string;
  /** Resolved backend model id (e.g. gpt-5.5, grok-4.5). */
  model?: string;
  intent?: string;
  source: AgentRunSourceView;
  application?: string;
  applicationTarget?: string;
  job?: NamespacedObjectReference;
  runnerPod?: NamespacedObjectReference;
  startedAt?: string;
  completedAt?: string;
  conditions?: Condition[];
  decision?: AgentRunDecision;
  reports?: AgentRunReport[];
  resolvedComposition?: ResolvedComposition;
  pullRequestURL?: string;
  error?: string;
  output?: string;
  archived: boolean;
}

export interface AgentRunListResponse {
  items: AgentRunView[];
}

export interface APIErrorBody {
  error?: {
    code?: string;
    message?: string;
  };
}

export type StreamEventType =
  | "snapshot"
  | "status"
  | "log"
  | "terminal"
  | "reset"
  | "error"
  | "complete";

export interface StreamLogLine {
  pod?: string;
  podUID?: string;
  timestamp?: string;
  line?: string;
}

export interface StreamEnvelope {
  type?: StreamEventType | string;
  code?: string;
  message?: string;
  run?: AgentRunView;
  reason?: string;
  previousEventID?: string;
  pod?: string;
  podUID?: string;
  timestamp?: string;
  line?: string;
}

export interface ParsedSSEEvent {
  id?: string;
  event: string;
  data: string;
}
