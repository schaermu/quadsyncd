/** API client for quadsyncd JSON API. */

export interface OverviewRepo {
  url: string;
  ref?: string;
  sha?: string;
}

export interface OverviewResponse {
  repositories: OverviewRepo[];
  last_run_id?: string;
  last_run_status?: string;
}

export interface RunMeta {
  id: string;
  kind: "sync" | "plan";
  trigger: "timer" | "cli" | "webhook" | "startup" | "ui";
  started_at: string;
  ended_at?: string;
  status: "running" | "success" | "error";
  dry_run: boolean;
  revisions: Record<string, string>;
  conflicts: ConflictSummary[];
  summary?: Record<string, unknown>;
  error?: string;
}

export interface ConflictSummary {
  merge_key: string;
  winner: EffectiveItemSummary;
  losers: EffectiveItemSummary[];
}

export interface EffectiveItemSummary {
  merge_key: string;
  source_repo: string;
  source_ref: string;
  source_sha: string;
}

export interface RunsListResponse {
  items: RunMeta[];
  next_cursor?: string;
}

export interface LogEntry {
  time?: string;
  level?: string;
  msg?: string;
  [key: string]: unknown;
}

export interface RunLogsResponse {
  items: LogEntry[];
  next_cursor?: string;
}

export interface PlanOp {
  op: "add" | "update" | "delete";
  path: string;
  unit?: string;
  source_repo?: string;
  source_ref?: string;
  source_sha?: string;
  before_path?: string;
  after_path?: string;
}

export interface Plan {
  requested: { repo_url?: string; ref?: string; commit?: string };
  conflicts: ConflictSummary[];
  ops: PlanOp[];
}

export interface UnitInfo {
  name: string;
  source_path: string;
  source_repo?: string;
  source_ref?: string;
  source_sha?: string;
  hash: string;
}

export interface UnitsResponse {
  items: UnitInfo[];
}

export interface TimerInfo {
  unit: string;
  active: boolean;
}

export interface PlanTriggerResponse {
  run_id: string;
  status?: string;
  error?: string;
}

function getCsrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, init);
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`API ${resp.status}: ${body}`);
  }
  return resp.json() as Promise<T>;
}

export function fetchOverview(): Promise<OverviewResponse> {
  return apiFetch("/api/overview");
}

export function fetchRuns(
  limit = 20,
  cursor?: string,
): Promise<RunsListResponse> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set("cursor", cursor);
  return apiFetch(`/api/runs?${params}`);
}

export function fetchRunDetail(id: string): Promise<RunMeta> {
  return apiFetch(`/api/runs/${encodeURIComponent(id)}`);
}

export interface LogFilters {
  level?: string;
  component?: string;
  q?: string;
  since?: string;
  limit?: number;
  cursor?: string;
}

export function fetchRunLogs(
  id: string,
  filters: LogFilters = {},
): Promise<RunLogsResponse> {
  const params = new URLSearchParams();
  if (filters.level) params.set("level", filters.level);
  if (filters.component) params.set("component", filters.component);
  if (filters.q) params.set("q", filters.q);
  if (filters.since) params.set("since", filters.since);
  if (filters.limit) params.set("limit", String(filters.limit));
  if (filters.cursor) params.set("cursor", filters.cursor);
  return apiFetch(`/api/runs/${encodeURIComponent(id)}/logs?${params}`);
}

export function fetchRunPlan(id: string): Promise<Plan> {
  return apiFetch(`/api/runs/${encodeURIComponent(id)}/plan`);
}

export function fetchUnits(): Promise<UnitsResponse> {
  return apiFetch("/api/units");
}

export function fetchTimer(): Promise<TimerInfo> {
  return apiFetch("/api/timer");
}

export function triggerPlan(): Promise<PlanTriggerResponse> {
  return apiFetch("/api/plan", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": getCsrfToken(),
    },
  });
}
