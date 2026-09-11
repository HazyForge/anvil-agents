export type ToolKind = "harness" | "controlPlane" | "cluster" | "workstation";

export interface Discovered {
  id: string;
  displayName: string;
  kind: ToolKind;
  backend?: string;
  binaries: string[];
  present: boolean;
  path?: string;
  version?: string;
  versionError?: string;
  authFileHint?: string;
  clusterHint?: string;
  notes?: string;
}

export interface ContextInfo {
  name: string;
  cluster?: string;
  user?: string;
  namespace?: string;
  current: boolean;
}

export interface OperatorStatus {
  reachable: boolean;
  apiGroupPresent: boolean;
  groupVersion?: string;
  message?: string;
}

export interface ConsoleStatus {
  url?: string;
  reachable: boolean;
  message?: string;
}

export interface Prefs {
  kubeconfig?: string;
  kubeContext?: string;
  consoleURL?: string;
}

export interface ClusterSnapshot {
  kubeconfig?: string;
  currentContext?: string;
  selectedContext?: string;
  namespace?: string;
  contexts: ContextInfo[];
  operator: OperatorStatus;
  console: ConsoleStatus;
  message?: string;
}

export interface ChatView {
  standingChatPath: string;
  councilChat: string;
  message: string;
}

export interface Snapshot {
  productTitle: string;
  listenAddr?: string;
  prefs: Prefs;
  harnesses: Discovered[];
  cluster: ClusterSnapshot;
  chat: ChatView;
}
