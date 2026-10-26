import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, Card, clamp, ease, SceneTitle, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// A trace of one request with a fallback, with the span names of
// docs/observability.md. The durations are from the dashboard demo log:
// the flaky target returns 529 at once, and the backup answers in 760 ms.
type Span = {name: string; depth: number; from: number; to: number; col: string; note?: string; err?: boolean};
const TOTAL = 762;
const SPANS: Span[] = [
  {name: 'POST /v1/messages', depth: 0, from: 0, to: 762, col: c.blue, note: '762 ms'},
  {name: 'auth', depth: 1, from: 0, to: 3, col: '#8a8de8'},
  {name: 'preflight', depth: 1, from: 3, to: 6, col: '#8a8de8'},
  {name: 'limit check', depth: 1, from: 6, to: 9, col: '#8a8de8'},
  {name: 'chat claude-haiku-5-5', depth: 1, from: 9, to: 14, col: c.critical, note: 'fake-flaky · 529', err: true},
  {name: 'chat claude-haiku-5-5', depth: 1, from: 14, to: 762, col: c.good, note: 'fake-backup · 200'},
];

const METRICS: [string, string][] = [
  ['tollgate_fallbacks_total', 'counter'],
  ['tollgate_breaker_state', 'gauge'],
  ['tollgate_time_to_first_token_seconds', 'histogram'],
  ['tollgate_gateway_overhead_seconds', 'histogram'],
  ['tollgate_cost_micro_usd_total', 'counter'],
];

export const Observability: React.FC = () => {
  const frame = useCurrentFrame();
  const foot = interpolate(frame, [200, 225], [0, 1], {...clamp, easing: ease});
  return (
    <AbsoluteFill>
      <Background />
      <SceneTitle n="07" label="Observability" title="Every attempt traced. Every dollar counted." highlight={{traced: c.blueLight, counted: c.blueLight}} />
      <Trace />
      <Metrics />
      <div style={{position: 'absolute', left: 0, right: 0, top: 952, textAlign: 'center', fontFamily: monoFont, fontSize: 29, color: c.muted, opacity: foot, transform: `translateY(${(1 - foot) * 16}px)`}}>
        Prometheus<Dot />OpenTelemetry (OTLP)<Dot />Grafana dashboard<Dot />JSONL usage events
      </div>
    </AbsoluteFill>
  );
};

const Dot: React.FC = () => <span style={{color: c.blueLight}}> · </span>;

const Trace: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(8);
  const LABEL = 410;
  const BAR = 1000 - 80 - LABEL;
  // A log scale for the first 20 ms, so that the short spans can be seen.
  const x = (ms: number) => (ms <= 20 ? (ms / 20) * 0.22 : 0.22 + ((ms - 20) / (TOTAL - 20)) * 0.78) * BAR;
  const retryAt = 40 + 4 * 14;
  return (
    <div style={{position: 'absolute', left: 120, top: 300, width: 1000, opacity: p, transform: `translateY(${(1 - p) * 40}px)`}}>
      <Card style={{padding: '30px 40px', height: 610, boxSizing: 'border-box'}}>
        <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'baseline'}}>
          <div style={{fontFamily: font, fontSize: 36, fontWeight: 700, color: c.text}}>Trace</div>
          <div style={{fontFamily: monoFont, fontSize: 25, color: c.muted}}>tollgate.attempts = 2</div>
        </div>
        <div style={{marginTop: 22}}>
          {SPANS.map((s, i) => {
            const q = interpolate(frame - 30 - i * 14, [0, 18], [0, 1], {...clamp, easing: ease});
            const w = Math.max(6, x(s.to) - x(s.from));
            return (
              <div key={i} style={{display: 'flex', alignItems: 'center', height: 74, borderTop: i ? `1px solid ${c.border}` : 'none', opacity: q}}>
                <div style={{width: LABEL, paddingLeft: s.depth * 28, fontFamily: monoFont, fontSize: 26, color: s.err ? '#ff8a8a' : c.text, whiteSpace: 'nowrap', overflow: 'hidden'}}>
                  {s.name}
                </div>
                <div style={{position: 'relative', flex: 1, height: 74}}>
                  <div
                    style={{
                      position: 'absolute',
                      left: x(s.from),
                      top: 22,
                      width: w * q,
                      height: 30,
                      borderRadius: 6,
                      background: s.col,
                      boxShadow: s.err ? `0 0 18px ${c.critical}` : undefined,
                    }}
                  />
                  {s.note && (
                    <div
                      style={{
                        position: 'absolute',
                        top: 22,
                        lineHeight: '30px',
                        fontFamily: monoFont,
                        fontSize: 24,
                        fontWeight: 700,
                        whiteSpace: 'nowrap',
                        color: s.err ? '#ff8a8a' : '#fff',
                        ...(w > 260 ? {left: x(s.from) + 14} : {left: x(s.from) + w + 12}),
                        opacity: q,
                      }}
                    >
                      {s.note}
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
        <div style={{marginTop: 14, fontFamily: monoFont, fontSize: 25, color: c.warning, opacity: interpolate(frame - retryAt - 10, [0, 14], [0, 1], clamp)}}>
          event: retry · fake-flaky → fake-backup · overloaded
        </div>
      </Card>
    </div>
  );
};

const Metrics: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(18);
  return (
    <div style={{position: 'absolute', left: 1150, top: 300, width: 650, opacity: p, transform: `translateY(${(1 - p) * 40}px)`}}>
      <Card style={{padding: '30px 36px', height: 610, boxSizing: 'border-box'}}>
        <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'baseline'}}>
          <div style={{fontFamily: font, fontSize: 36, fontWeight: 700, color: c.text}}>Metrics</div>
          <div style={{fontFamily: monoFont, fontSize: 25, color: c.muted}}>GET /metrics</div>
        </div>
        <div style={{marginTop: 18}}>
          {METRICS.map(([name, kind], i) => {
            const q = interpolate(frame - 60 - i * 10, [0, 16], [0, 1], {...clamp, easing: ease});
            return (
              <div key={name} style={{padding: '16px 0', borderTop: i ? `1px solid ${c.border}` : 'none', opacity: q, transform: `translateX(${(1 - q) * 20}px)`}}>
                <div style={{fontFamily: monoFont, fontSize: 22, color: c.text, whiteSpace: 'nowrap'}}>{name}</div>
                <div style={{fontFamily: monoFont, fontSize: 24, color: c.blueLight, marginTop: 4}}>{kind}</div>
              </div>
            );
          })}
        </div>
      </Card>
    </div>
  );
};
