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
    name?: string;
    namespace?: string;
    phase?: string;
    backend?: string;
    error?: string;
    decision?: { action?: string; summary?: string };
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
