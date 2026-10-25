import type { ReactNode } from 'react';

export function Tile({ label, value, note, hero = false }: { label: string; value: string; note?: ReactNode; hero?: boolean }) {
  return (
    <div className={hero ? 'tile hero' : 'tile'}>
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {note && <div className="note">{note}</div>}
    </div>
  );
}

// Meter shows the used part of a budget. The fill changes to the warning
// color at the warning ratio and to the critical color at the limit.
export function Meter({ ratio, warnRatio = 0.8, label }: { ratio: number; warnRatio?: number; label: string }) {
  const cls = ratio >= 1 ? 'meter over' : ratio >= warnRatio ? 'meter warn' : 'meter';
  return (
    <div className={cls} role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(ratio * 100)} aria-label={label}>
      <div style={{ width: `${Math.min(ratio, 1) * 100}%` }} />
    </div>
  );
}

const breakerStyle: Record<string, { color: string; label: string; icon: string }> = {
  closed: { color: 'var(--good)', label: 'Closed', icon: '✓' },
  half_open: { color: 'var(--warning)', label: 'Half-open', icon: '!' },
  open: { color: 'var(--critical)', label: 'Open', icon: '×' },
  unknown: { color: 'var(--muted)', label: 'Unknown', icon: '?' },
};

// BreakerBadge shows a breaker state with a color, an icon and a word, so
// the color never carries the state alone.
export function BreakerBadge({ state }: { state: string }) {
  const s = breakerStyle[state] ?? breakerStyle.unknown!;
  return (
    <span className="badge">
      <span className="dot" style={{ background: s.color }} aria-hidden="true" />
      <span aria-hidden="true">{s.icon}</span>
      {s.label}
    </span>
  );
}

export function ErrorNotice({ error }: { error: Error }) {
  return (
    <div className="notice error" role="alert">
      <strong>Tollgate cannot load this page.</strong> {error.message}
    </div>
  );
}
