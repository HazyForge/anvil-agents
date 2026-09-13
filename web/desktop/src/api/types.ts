export type ToolKind = "harness" | "workstation";

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
  delegatable?: boolean;
  notes?: string;
}

export interface Prefs {
  apiOrigin?: string;
}

export interface APIStatus {
  origin?: string;
  reachable: boolean;
  message?: string;
}

export interface WrapperTool {
  id: string;
  displayName: string;
  notes: string;
}

export interface WrapperInfo {
  tools: WrapperTool[];
  message: string;
}

export interface Snapshot {
  productTitle: string;
  listenAddr?: string;
  prefs: Prefs;
  api: APIStatus;
  harnesses: Discovered[];
  wrapper: WrapperInfo;
}

export interface DelegateResult {
  harness: string;
  path?: string;
  exitCode: number;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}
