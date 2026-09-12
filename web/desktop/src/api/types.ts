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
  source?: "native" | "wsl";
  wslDistro?: string;
}

export interface Prefs {
  apiOrigin?: string;
  harnessTarget?: "native" | "wsl" | "";
  wslDistro?: string;
}

export interface APIStatus {
  origin?: string;
  reachable: boolean;
  message?: string;
}

export interface WSLStatus {
  available: boolean;
  insideWSL?: boolean;
  executable?: string;
  defaultDistro?: string;
  distros?: string[];
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
  wsl: WSLStatus;
  harnessTarget: "native" | "wsl";
}

export interface DelegateResult {
  harness: string;
  target?: string;
  wslDistro?: string;
  path?: string;
  exitCode: number;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}
