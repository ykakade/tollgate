// Number and time formats of the dashboard.

const compact = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 });
const whole = new Intl.NumberFormat('en');

export function usd(micro: number): string {
  const v = micro / 1e6;
  if (v === 0) return '$0';
  const abs = Math.abs(v);
  const digits = abs >= 100 ? 0 : abs >= 1 ? 2 : abs >= 0.01 ? 3 : 4;
  return `$${v.toFixed(digits)}`;
}

export function count(n: number): string {
  return Math.abs(n) >= 10000 ? compact.format(n) : whole.format(n);
}

export function percent(ratio: number, digits = 0): string {
  if (!Number.isFinite(ratio)) return '-';
  return `${(ratio * 100).toFixed(digits)}%`;
}

export function ms(v: number | null | undefined): string {
  if (v == null) return '-';
  return v >= 1000 ? `${(v / 1000).toFixed(v >= 10000 ? 0 : 1)} s` : `${v} ms`;
}

export function minutes(v: number | null | undefined): string {
  if (v == null) return '-';
  if (v < 1) return 'under 1 min';
  if (v < 120) return `${Math.round(v)} min`;
  return `${(v / 60).toFixed(1)} h`;
}

export function time(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function dateTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

// cacheReadShare is the part of the prompt tokens that the provider read
// from its prompt cache.
export function cacheReadShare(t: { input_tokens: number; cache_read_tokens: number; cache_write_tokens: number }): number {
  const prompt = t.input_tokens + t.cache_read_tokens + t.cache_write_tokens;
  return prompt === 0 ? NaN : t.cache_read_tokens / prompt;
}

export function shortSession(id: string): string {
  return id.startsWith('auto-') ? `auto-${id.slice(5, 13)}` : id;
}
