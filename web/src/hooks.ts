import { useCallback, useEffect, useRef, useState } from 'react';

import { UnauthorizedError } from './api';

// refreshMs is the time between two reads of the admin API.
const refreshMs = 10_000;

export interface Loaded<T> {
  data: T | undefined;
  error: Error | undefined;
  // loading is true while a read runs. The last data stays visible.
  loading: boolean;
}

// usePoll calls load now, again when key changes, and every 10 s. It keeps
// the last data while a new read runs, so the page does not jump.
export function usePoll<T>(load: () => Promise<T>, key: string, onUnauthorized: () => void): Loaded<T> {
  const [state, setState] = useState<Loaded<T>>({ data: undefined, error: undefined, loading: true });
  const loadRef = useRef(load);
  loadRef.current = load;
  const run = useCallback(
    async (signal: { stale: boolean }) => {
      setState((s) => ({ ...s, loading: true }));
      try {
        const data = await loadRef.current();
        if (!signal.stale) setState({ data, error: undefined, loading: false });
      } catch (e) {
        if (signal.stale) return;
        if (e instanceof UnauthorizedError) onUnauthorized();
        setState((s) => ({ ...s, error: e instanceof Error ? e : new Error(String(e)), loading: false }));
      }
    },
    [onUnauthorized],
  );
  useEffect(() => {
    const signal = { stale: false };
    void run(signal);
    const id = setInterval(() => void run(signal), refreshMs);
    return () => {
      signal.stale = true;
      clearInterval(id);
    };
  }, [key, run]);
  return state;
}

export type RangeName = '24h' | '7d' | '30d';

export interface Range {
  name: RangeName;
  from: Date;
  to: Date;
  // bucket is the group_by value for charts over time.
  bucket: 'hour' | 'day';
}

const rangeHours: Record<RangeName, number> = { '24h': 24, '7d': 24 * 7, '30d': 24 * 30 };

export function makeRange(name: RangeName, now = new Date()): Range {
  const to = new Date(now.getTime() + 1000);
  const from = new Date(now.getTime() - rangeHours[name] * 3600_000);
  return { name, from, to, bucket: name === '30d' ? 'day' : 'hour' };
}

// buckets returns the keys of all time buckets of a range, in the format
// of the admin API.
export function buckets(r: Range): string[] {
  const out: string[] = [];
  const d = new Date(r.from);
  if (r.bucket === 'hour') {
    d.setUTCMinutes(0, 0, 0);
    while (d < r.to) {
      out.push(d.toISOString().replace('.000Z', 'Z'));
      d.setUTCHours(d.getUTCHours() + 1);
    }
  } else {
    d.setUTCHours(0, 0, 0, 0);
    while (d < r.to) {
      out.push(d.toISOString().slice(0, 10));
      d.setUTCDate(d.getUTCDate() + 1);
    }
  }
  return out;
}

export function bucketLabel(key: string, bucket: 'hour' | 'day', range: RangeName): string {
  if (bucket === 'day') {
    return new Date(`${key}T00:00:00Z`).toLocaleDateString([], { month: 'short', day: 'numeric' });
  }
  const d = new Date(key);
  if (range === '24h') return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  return d.toLocaleString([], { weekday: 'short', hour: '2-digit' });
}

export type Theme = 'system' | 'light' | 'dark';

const themeKey = 'tollgate-theme';

export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem(themeKey) as Theme | null) ?? 'system');
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'system') root.removeAttribute('data-theme');
    else root.setAttribute('data-theme', theme);
    localStorage.setItem(themeKey, theme);
  }, [theme]);
  return [theme, setTheme];
}

// useHash returns the path in the URL fragment, for example "/sessions".
export function useHash(): string {
  const [hash, setHash] = useState(() => window.location.hash.slice(1) || '/');
  useEffect(() => {
    const on = () => setHash(window.location.hash.slice(1) || '/');
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return hash;
}
