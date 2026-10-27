/**
 * One chat host client (protocol v1, docs/one-chat-remote.md).
 *
 * The page talks to its own Desktop host at /local/v1/onechat/connect; the
 * Desktop host adds the host token and forwards to `onechat serve` on
 * loopback, so the page never holds the token. The same frames later travel
 * through the relay to the iPhone.
 */
export const ONECHAT_PROTOCOL = 'anvil.onechat.v1';

export type OnechatMessage = {role: 'user' | 'assistant'; text: string; at: string};

export type OnechatSession = {
  id: string;
  location: 'local' | 'cloud';
  setupId: string;
  harness: string;
  title: string;
  summary: string;
  state: string;
  createdAt: string;
  updatedAt: string;
  workdir?: string;
  namespace?: string;
  profile?: string;
  transcript?: OnechatMessage[];
};

export type OnechatAction = {
  type: string;
  session_id?: string;
  setup_id?: string;
  title?: string;
  workdir?: string;
  message?: string;
  text?: string;
};

export type OnechatDecision = {reasoning_summary?: string; confidence?: number; actions?: OnechatAction[]};

export type OnechatActionResult = {
  action: OnechatAction;
  sessionId: string;
  sentText?: string;
  reply?: string;
  state?: string;
  error?: string;
  harness?: string;
  durationMs?: number;
  truncated?: boolean;
};

export type OnechatEvent = {
  type: 'event';
  event: string;
  turnId?: string;
  seq?: number;
  at: string;
  body: Record<string, unknown>;
};

export type OnechatRunningTurn = {
  turnId: string;
  text: string;
  startedAt: string;
  requestId: string;
  by?: {name?: string; kind?: string};
  events: OnechatEvent[];
};

export type OnechatStatus = {state: 'idle' | 'busy'; turn: OnechatRunningTurn | null; sessionsRefreshedAt?: string};

export type OnechatHello = {protocol: number; host: {id: string; name: string; version: string}; status: OnechatStatus};

export type OnechatTurnSummary = {turnId: string; at: string; message: string; answer: string; error?: string; actionCount: number};

/** What the Desktop host knows about the one chat host on this machine. */
export type OnechatHostStatus = {configured: boolean; running: boolean; listen?: string; hostId?: string; version?: string; message?: string};

export class OnechatError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'OnechatError';
    this.code = code;
  }
}

export async function fetchOnechatHostStatus(signal?: AbortSignal): Promise<OnechatHostStatus> {
  const response = await fetch('/local/v1/onechat/status', {cache: 'no-store', signal});
  if (!response.ok) throw new OnechatError(`http_${response.status}`, `Desktop could not check the one chat host (HTTP ${response.status}).`);
  return response.json() as Promise<OnechatHostStatus>;
}

type Pending = {resolve: (body: Record<string, unknown>) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout>};

export type OnechatClientHandlers = {
  onHello: (hello: OnechatHello) => void;
  onEvent: (event: OnechatEvent) => void;
  /** The connection closed; `clean` is false when it failed or dropped. */
  onClose: (clean: boolean) => void;
};

/** One WebSocket to the host: requests matched by id, events broadcast. */
export class OnechatClient {
  private socket: WebSocket | null = null;
  private pending = new Map<string, Pending>();
  private closed = false;
  private readonly handlers: OnechatClientHandlers;
  private readonly clientName: string;

  constructor(handlers: OnechatClientHandlers, clientName = 'Anvil Agents Desktop') {
    this.handlers = handlers;
    this.clientName = clientName;
  }

  connect(): void {
    const url = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/local/v1/onechat/connect`;
    const socket = new WebSocket(url, ONECHAT_PROTOCOL);
    this.socket = socket;
    socket.onopen = () => {
      void this.request('hello', {client: {name: this.clientName, kind: 'desktop'}})
        .then(body => this.handlers.onHello(body as unknown as OnechatHello))
        .catch(() => socket.close());
    };
    socket.onmessage = message => {
      if (typeof message.data !== 'string') return;
      let frame: Record<string, unknown>;
      try {
        frame = JSON.parse(message.data) as Record<string, unknown>;
      } catch {
        return;
      }
      if (frame.type === 'event' && typeof frame.event === 'string') {
        this.handlers.onEvent(frame as unknown as OnechatEvent);
      } else if (frame.type === 'response' && typeof frame.id === 'string') {
        const waiting = this.pending.get(frame.id);
        if (!waiting) return;
        this.pending.delete(frame.id);
        clearTimeout(waiting.timer);
        if (frame.ok === true) waiting.resolve((frame.body ?? {}) as Record<string, unknown>);
        else {
          const error = (frame.error ?? {}) as {code?: string; message?: string};
          waiting.reject(new OnechatError(error.code || 'internal', error.message || 'The one chat host rejected the request.'));
        }
      }
    };
    socket.onclose = event => {
      this.failPending(new OnechatError('closed', 'The connection to the one chat host closed.'));
      if (!this.closed) this.handlers.onClose(event.wasClean);
    };
  }

  request(op: string, body: Record<string, unknown> = {}, timeoutMs = 30_000): Promise<Record<string, unknown>> {
    const socket = this.socket;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return Promise.reject(new OnechatError('closed', 'Not connected to the one chat host.'));
    }
    const id = crypto.randomUUID();
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new OnechatError('timeout', `The one chat host did not answer ${op} in time.`));
      }, timeoutMs);
      this.pending.set(id, {resolve, reject, timer});
      socket.send(JSON.stringify({type: 'request', id, op, body}));
    });
  }

  close(): void {
    this.closed = true;
    this.failPending(new OnechatError('closed', 'Disconnected.'));
    this.socket?.close(1000, 'client closed');
  }

  private failPending(error: Error) {
    for (const [id, waiting] of this.pending) {
      clearTimeout(waiting.timer);
      waiting.reject(error);
      this.pending.delete(id);
    }
  }
}
