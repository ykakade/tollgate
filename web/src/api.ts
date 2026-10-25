// Types and calls of the Tollgate admin API. See docs/admin-api.md.

export interface Totals {
  requests: number;
  errors: number;
  rejected: number;
  cache_hits: number;
  fallbacks: number;
  target_switches: number;
  budget_cutoffs: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_micro_usd: number;
  cache_loss_micro_usd: number;
  ttft_p50_ms: number | null;
  ttft_p95_ms: number | null;
}

export interface Group extends Totals {
  key: string;
}

export interface UsageResponse {
  from: string;
  to: string;
  group_by: string;
  groups: Group[];
}

export interface Projection {
  limit_micro_usd: number;
  mode: string;
  spend_micro_usd: number;
  remaining_micro_usd: number;
  used_ratio: number;
  warn_ratio: number;
  avg_cost_micro_usd: number;
  requests_left: number | null;
  spend_per_minute_micro_usd: number;
  minutes_left: number | null;
}

export interface Session extends Totals {
  tenant_id: string;
  session_id: string;
  derived: boolean;
  first: string;
  last: string;
  provider: string;
  model: string;
  budget: Projection | null;
}

export interface SessionsResponse {
  total: number;
  sessions: Session[];
}

export interface UsageEvent {
  request_id: string;
  ts: string;
  tenant_id: string;
  key_id: string;
  session_id?: string;
  route_model: string;
  provider: string;
  provider_model: string;
  status: number;
  attempts: number;
  cache: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_micro_usd: number;
  ttft_ms: number | null;
  duration_ms: number;
  target_switched?: boolean;
  cache_loss_micro_usd?: number;
  budget_cutoff?: boolean;
  redacted?: number;
}

export interface SessionResponse {
  session: Session;
  events: UsageEvent[];
}

export interface Breaker {
  provider: string;
  model: string;
  region?: string;
  state: 'closed' | 'half_open' | 'open' | 'unknown';
}

export interface Key extends Totals {
  id: string;
}

export interface Tenant {
  id: string;
  models: string[];
  limits: { requests_per_minute: number; tokens_per_minute: number };
  budgets: { window: string; limit_usd: string; mode: string; spend_micro_usd?: number }[];
  redaction: string[];
  keys: Key[];
}

export interface Info {
  version: string;
  started: string;
  store: string;
  usage_sink: string;
  derive_sessions: boolean;
  sticky_routing: boolean;
  index_capacity: number;
  events: number;
}

// TokenStore keeps the admin token for this browser tab only.
const tokenKey = 'tollgate-admin-token';

export function getToken(): string {
  return sessionStorage.getItem(tokenKey) ?? '';
}

export function setToken(token: string): void {
  if (token) sessionStorage.setItem(tokenKey, token);
  else sessionStorage.removeItem(tokenKey);
}

export class UnauthorizedError extends Error {}

export async function get<T>(path: string, params: Record<string, string | undefined> = {}): Promise<T> {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const url = q.size > 0 ? `${path}?${q}` : path;
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const resp = await fetch(url, { headers });
  if (resp.status === 401) throw new UnauthorizedError('The admin token is missing or not correct.');
  if (!resp.ok) {
    let msg = `The admin API returned status ${resp.status}.`;
    try {
      const body = (await resp.json()) as { error?: string };
      if (body.error) msg = body.error;
    } catch {
      // Keep the status message.
    }
    throw new Error(msg);
  }
  return (await resp.json()) as T;
}
