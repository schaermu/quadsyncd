/** SSE client for quadsyncd live events. */

import type { ConflictSummary, LogEntry } from "./api";

export type SSEEventKind =
  | "run_started"
  | "run_updated"
  | "log_appended"
  | "plan_ready";

export interface SSEEventPayload {
  run_id: string;
  kind?: string;
  status?: string;
  trigger?: string;
  dry_run?: boolean;
  started_at?: string;
  ended_at?: string;
  revisions?: Record<string, string>;
  conflicts?: ConflictSummary[];
  lines?: LogEntry[];
}

export type SSECallback = (kind: SSEEventKind, payload: SSEEventPayload) => void;

let eventSource: EventSource | null = null;
let listeners: SSECallback[] = [];
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let connectionState: "connected" | "connecting" | "disconnected" =
  "disconnected";
let stateListeners: Array<
  (state: "connected" | "connecting" | "disconnected") => void
> = [];

function setConnectionState(
  state: "connected" | "connecting" | "disconnected",
) {
  connectionState = state;
  stateListeners.forEach((cb) => cb(state));
}

export function getConnectionState() {
  return connectionState;
}

export function onConnectionStateChange(
  cb: (state: "connected" | "connecting" | "disconnected") => void,
): () => void {
  stateListeners.push(cb);
  return () => {
    stateListeners = stateListeners.filter((l) => l !== cb);
  };
}

export function connectSSE() {
  if (eventSource) return;
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  setConnectionState("connecting");

  const es = new EventSource("/api/events");
  eventSource = es;

  es.onopen = () => {
    reconnectTimer = null;
    setConnectionState("connected");
  };

  es.onerror = () => {
    setConnectionState("disconnected");
    es.close();
    eventSource = null;
    if (reconnectTimer === null) {
      // Reconnect after 3 seconds
      reconnectTimer = setTimeout(connectSSE, 3000);
    }
  };

  const eventKinds: SSEEventKind[] = [
    "run_started",
    "run_updated",
    "log_appended",
    "plan_ready",
  ];
  for (const kind of eventKinds) {
    es.addEventListener(kind, (e: MessageEvent) => {
      try {
        const payload = JSON.parse(e.data) as SSEEventPayload;
        listeners.forEach((cb) => cb(kind, payload));
      } catch {
        // ignore parse errors
      }
    });
  }
}

export function disconnectSSE() {
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
  setConnectionState("disconnected");
}

export function onSSEEvent(cb: SSECallback): () => void {
  listeners.push(cb);
  return () => {
    listeners = listeners.filter((l) => l !== cb);
  };
}
