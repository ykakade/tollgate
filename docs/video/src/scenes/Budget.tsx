import React from 'react';
import {AbsoluteFill, interpolate, interpolateColors, useCurrentFrame} from 'remotion';
import {Background, Card, clamp, ease, SceneTitle, shake, Terminal, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

const FULL = 200; // frame where the budget runs out
const STAMP = 214;

// The budgets are from examples/claude-code/tollgate.yaml.
const YAML: [string, string?][] = [
  ['tenants:'],
  ['  - id: ', 'personal'],
  ['    limits:'],
  ['      requests_per_minute: ', '120'],
  ['    budgets:'],
  ['      - window: ', 'month'],
  ['        limit_usd: ', '50'],
  ['      - window: ', 'session'],
  ['        limit_usd: ', '5'],
];

export const Budget: React.FC = () => {
  const frame = useCurrentFrame();
  const s = shake(frame, STAMP, 16, 12);
  return (
    <AbsoluteFill>
      <Background glow={frame > STAMP ? c.critical : c.blue} />
      <SceneTitle n="05" label="Limits and budgets" title="Hard budgets. No surprise bills." highlight={{Hard: c.warning, budgets: c.warning}} />
      <AbsoluteFill style={{transform: `translate(${s.x}px, ${s.y}px)`}}>
        <Config />
        <Meter />
      </AbsoluteFill>
    </AbsoluteFill>
  );
};

const Config: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(14);
  return (
    <div style={{position: 'absolute', left: 120, top: 300, width: 720, opacity: p, transform: `translateX(${(1 - p) * -60}px)`}}>
      <Terminal title="tollgate.yaml">
        <div style={{fontSize: 30, lineHeight: 1.6}}>
          {YAML.map(([k, v], i) => {
            const q = interpolate(frame - 26 - i * 6, [0, 12], [0, 1], {...clamp, easing: ease});
            const hot = v === '50' && frame > FULL;
            return (
              <div key={i} style={{opacity: q, transform: `translateX(${(1 - q) * 16}px)`, whiteSpace: 'pre'}}>
                <span style={{color: '#8fb8f0'}}>{k}</span>
                {v && (
                  <span
                    style={{
                      color: hot ? c.critical : '#f6c177',
                      background: hot ? `${c.critical}22` : undefined,
                      borderRadius: 6,
                      padding: hot ? '0 6px' : undefined,
                    }}
                  >
                    {v}
                  </span>
                )}
              </div>
            );
          })}
        </div>
      </Terminal>
    </div>
  );
};

const Meter: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(22);
  const fill = interpolate(frame, [60, FULL], [0, 1], {...clamp, easing: (t) => t * t * (3 - 2 * t)});
  const spend = fill * 50;
  const col = interpolateColors(fill, [0, 0.7, 0.85, 1], [c.blue, c.blue, c.warning, c.critical]);
  const stamp = useSpring(STAMP, {damping: 11, stiffness: 260, mass: 0.7});
  const msg = interpolate(frame, [STAMP + 18, STAMP + 40], [0, 1], {...clamp, easing: ease});
  const blocked = frame >= STAMP;

  // A short list of requests: each one adds spend, the last one gets 402.
  const rows = Array.from({length: 5}, (_, i) => 70 + i * 26).filter((f) => f <= frame);
  return (
    <div style={{position: 'absolute', left: 900, top: 300, width: 900, opacity: p, transform: `translateX(${(1 - p) * 60}px)`}}>
      <Card style={{padding: '34px 40px'}} glow={blocked ? `${c.critical}33` : undefined}>
        <div style={{fontFamily: font, fontSize: 32, color: c.muted}}>Month budget · tenant personal</div>
        <div style={{display: 'flex', alignItems: 'baseline', gap: 18, marginTop: 6}}>
          <div style={{fontFamily: font, fontWeight: 800, fontSize: 110, letterSpacing: '-0.04em', color: c.text, fontVariantNumeric: 'tabular-nums'}}>${spend.toFixed(2)}</div>
          <div style={{fontFamily: font, fontSize: 34, color: c.muted}}>of $50.00 (hard)</div>
        </div>
        <div style={{height: 18, borderRadius: 9, background: 'rgba(255,255,255,0.07)', marginTop: 10, overflow: 'hidden'}}>
          <div style={{width: `${fill * 100}%`, height: '100%', borderRadius: 9, background: col, boxShadow: `0 0 24px ${col}`}} />
        </div>
        <div style={{marginTop: 22, fontFamily: monoFont, fontSize: 28, lineHeight: 1.5, minHeight: 6 * 42}}>
          {rows.map((f, i) => {
            const q = interpolate(frame - f, [0, 10], [0, 1], {...clamp, easing: ease});
            return (
              <div key={i} style={{display: 'flex', justifyContent: 'space-between', opacity: q * (blocked ? 0.35 : 1), color: c.muted}}>
                <span>POST /v1/messages</span>
                <span style={{color: c.good}}>200</span>
              </div>
            );
          })}
          {blocked && (
            <div style={{display: 'flex', justifyContent: 'space-between', color: c.text, fontWeight: 700}}>
              <span>POST /v1/messages</span>
              <span style={{color: c.critical}}>402</span>
            </div>
          )}
        </div>
      </Card>

      {/* The stamp. */}
      <div
        style={{
          position: 'absolute',
          left: 60,
          top: 290,
          padding: '20px 40px',
          border: `6px solid ${c.critical}`,
          borderRadius: 18,
          color: c.critical,
          background: 'rgba(30,8,8,0.88)',
          fontFamily: font,
          fontWeight: 800,
          fontSize: 66,
          letterSpacing: '-0.02em',
          whiteSpace: 'nowrap',
          boxShadow: `0 0 80px ${c.critical}66`,
          opacity: blocked ? 1 : 0,
          transform: `rotate(-7deg) scale(${2.4 - 1.4 * stamp})`,
        }}
      >
        402 Payment Required
      </div>
      <div
        style={{
          position: 'absolute',
          left: 0,
          right: 0,
          top: 600,
          fontFamily: monoFont,
          fontSize: 26,
          lineHeight: 1.6,
          color: c.muted,
          opacity: msg,
          transform: `translateY(${(1 - msg) * 16}px)`,
        }}
      >
        <span style={{color: '#8b8a85'}}>"type":</span> <span style={{color: '#ff8a8a'}}>"budget_exceeded"</span>
        <br />
        <span style={{color: c.muted}}>The Anthropic and OpenAI SDKs do not retry it.</span>
      </div>
    </div>
  );
};
