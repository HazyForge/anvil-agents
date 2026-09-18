export type StreamEventType =
  | "snapshot"
  | "status"
  | "log"
  | "terminal"
  | "reset"
  | "error"
  | "complete";

export interface StreamEnvelope {
  type?: StreamEventType | string;
  code?: string;
  message?: string;
  run?: {
    job?: {name?: string};
    runnerPod?: {name?: string};
    runnerState?: import('./runnerState').RunnerState;
    name?: string;
    namespace?: string;
    phase?: string;
    backend?: string;
    error?: string;
    output?: string;
    conditions?: { type?: string; status?: string; reason?: string; message?: string }[];
    decision?: { action?: string; summary?: string };
    reports?: { type?: string; summary?: string; detail?: string }[];
  };
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
