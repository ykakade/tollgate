import React from 'react';
import {AbsoluteFill, interpolate, random, useCurrentFrame} from 'remotion';
import {Background, Card, clamp, ease, SceneTitle, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// A circuit breaker opens after 20 attempts with a failure ratio of at
// least 0.5 (the defaults in docs/reliability.md), then tests the target.
const ATTEMPTS = 20;
const FILL = [40, 200];
const OPEN = 210;
const HALF = 300;
const CLOSE = 350;

// The retry rules table of docs/reliability.md.
const RULES: [string, string, string][] = [
  ['429', 'Next target, after retry-after', c.warning],
  ['529', 'Next target', c.critical],
  ['5xx', 'Same target once, then next', c.critical],
  ['Timeout', 'Next target', c.warning],
  ['Other 4xx', 'Sent to the client as is', c.muted],
];

export const Resilience: React.FC = () => {
  const frame = useCurrentFrame();
  const foot = interpolate(frame, [CLOSE + 10, CLOSE + 34], [0, 1], {...clamp, easing: ease});
  return (
    <AbsoluteFill>
      <Background glow={frame >= OPEN && frame < CLOSE ? c.critical : c.blue} />
      <SceneTitle n="04" label="Resilience" title="Circuit breakers. Retry rules. A retry budget." highlight={{breakers: c.blueLight}} />
      <Breaker />
      <Rules />
      <div style={{position: 'absolute', left: 120, right: 120, top: 930, display: 'flex', justifyContent: 'center', gap: 60, fontFamily: monoFont, fontSize: 29, color: c.muted, opacity: foot, transform: `translateY(${(1 - foot) * 16}px)`}}>
        <span>
          retry budget: <span style={{color: c.text}}>10 + 10% of recent requests</span>
        </span>
        <span>
          all targets blocked: <span style={{color: c.text}}>fast 502 + retry-after</span>
        </span>
      </div>
    </AbsoluteFill>
  );
};

const Breaker: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(10);
  const n = Math.floor(interpolate(frame, FILL, [0, ATTEMPTS], clamp));
  // Fixed pattern: failures grow toward the end, 11 of 20 fail.
  const fails = Array.from({length: ATTEMPTS}, (_, i) => i >= 6 && (i % 2 === 0 || i > 14 || random(`f${i}`) < 0.2));
  const failed = fails.slice(0, n).filter(Boolean).length;
  const ratio = n === 0 ? 0 : failed / n;
  const state = frame >= CLOSE ? 'closed' : frame >= HALF ? 'half-open' : frame >= OPEN ? 'open' : 'closed';
  const openLeft = interpolate(frame, [OPEN, HALF], [30, 0], clamp);
  const testT = interpolate(frame, [HALF + 8, CLOSE - 4], [0, 1], {...clamp, easing: ease});

  return (
    <div style={{position: 'absolute', left: 120, top: 300, width: 900, opacity: p, transform: `translateY(${(1 - p) * 40}px)`}}>
      <Card style={{padding: '34px 40px', height: 580, boxSizing: 'border-box'}} glow={state === 'open' ? `${c.critical}40` : undefined}>
        <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'baseline'}}>
          <div style={{fontFamily: font, fontSize: 38, fontWeight: 700, color: c.text}}>Circuit breaker</div>
          <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted}}>primary · claude-sonnet-5-5</div>
        </div>

        {/* State machine. */}
        <div style={{display: 'flex', alignItems: 'center', gap: 20, marginTop: 34}}>
          <StateNode label="Closed" active={state === 'closed'} color={c.good} />
          <Arrow />
          <StateNode label="Open" active={state === 'open'} color={c.critical} />
          <Arrow />
          <StateNode label="Half-open" active={state === 'half-open'} color={c.warning} />
        </div>

        {/* Recent attempts. */}
        <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted, marginTop: 40}}>Attempts in the recent minute</div>
        <div style={{display: 'flex', gap: 10, marginTop: 16}}>
          {fails.map((f, i) => (
            <div
              key={i}
              style={{
                width: 31,
                height: 44,
                borderRadius: 7,
                background: i < n ? (f ? c.critical : c.good) : 'rgba(255,255,255,0.06)',
                opacity: i < n ? (state === 'open' || state === 'half-open' ? 0.45 : 0.95) : 1,
              }}
            />
          ))}
        </div>
        <div style={{display: 'flex', alignItems: 'baseline', gap: 18, marginTop: 30, fontFamily: monoFont}}>
          <span style={{fontSize: 28, color: c.muted}}>failure ratio</span>
          <span style={{fontSize: 46, fontWeight: 700, color: ratio >= 0.5 && n >= ATTEMPTS ? c.critical : c.text, fontVariantNumeric: 'tabular-nums'}}>{ratio.toFixed(2)}</span>
          <span style={{fontSize: 28, color: c.muted}}>≥ 0.50 after {ATTEMPTS} attempts</span>
        </div>
        <div style={{marginTop: 22, fontFamily: font, fontSize: 32, fontWeight: 600, minHeight: 44}}>
          {state === 'open' && (
            <span style={{color: c.critical}}>
              Open: attempts skip this target for <span style={{fontVariantNumeric: 'tabular-nums'}}>{Math.ceil(openLeft)} s</span>
            </span>
          )}
          {state === 'half-open' && (
            <span style={{color: c.warning}}>
              Half-open: one test request{' '}
              <span style={{display: 'inline-block', width: 160, height: 10, borderRadius: 5, background: 'rgba(255,255,255,0.08)', verticalAlign: 'middle', marginLeft: 10}}>
                <span style={{display: 'block', width: `${testT * 100}%`, height: '100%', borderRadius: 5, background: c.warning}} />
              </span>
            </span>
          )}
          {state === 'closed' && frame >= CLOSE && <span style={{color: c.good}}>Test passed: closed again</span>}
        </div>
      </Card>
    </div>
  );
};

const StateNode: React.FC<{label: string; active: boolean; color: string}> = ({label, active, color}) => (
  <div
    style={{
      flex: 1,
      textAlign: 'center',
      padding: '18px 0',
      borderRadius: 16,
      fontFamily: font,
      fontSize: 32,
      fontWeight: 700,
      color: active ? color : '#6b6a66',
      background: active ? `${color}1f` : 'rgba(255,255,255,0.03)',
      border: `2px solid ${active ? color : 'rgba(255,255,255,0.08)'}`,
      boxShadow: active ? `0 0 34px ${color}55` : 'none',
    }}
  >
    {label}
  </div>
);

const Arrow: React.FC = () => (
  <svg width={34} height={24} viewBox="0 0 34 24">
    <path d="M2 12h28M22 4l8 8-8 8" fill="none" stroke="rgba(255,255,255,0.3)" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);

const Rules: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(18);
  return (
    <div style={{position: 'absolute', left: 1060, top: 300, width: 740, opacity: p, transform: `translateY(${(1 - p) * 40}px)`}}>
      <Card style={{padding: '34px 40px', height: 580, boxSizing: 'border-box'}}>
        <div style={{fontFamily: font, fontSize: 38, fontWeight: 700, color: c.text}}>Before the first byte</div>
        <div style={{fontFamily: font, fontSize: 28, color: c.muted, marginTop: 6}}>What Tollgate does with each failure</div>
        <div style={{marginTop: 22}}>
          {RULES.map(([code, action, col], i) => {
            const q = interpolate(frame - 40 - i * 14, [0, 16], [0, 1], {...clamp, easing: ease});
            return (
              <div key={code} style={{display: 'flex', alignItems: 'center', gap: 22, padding: '15px 0', borderTop: i ? `1px solid ${c.border}` : 'none', opacity: q, transform: `translateX(${(1 - q) * 24}px)`}}>
                <span style={{width: 180, flexShrink: 0, whiteSpace: 'nowrap', fontFamily: monoFont, fontSize: 29, fontWeight: 700, color: col}}>{code}</span>
                <span style={{fontFamily: font, fontSize: 30, color: c.text}}>{action}</span>
              </div>
            );
          })}
        </div>
      </Card>
    </div>
  );
};
