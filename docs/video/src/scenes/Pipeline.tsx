import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, Card, clamp, ease, SceneTitle, Terminal} from '../components/ui';
import {c, font, monoFont} from '../theme';

// The stages of one request, in the order of internal/server/proxy.go.
// Each stage shows a short description and a real snippet.
type Stage = {name: string; tag?: string; text: string; code: React.ReactNode; title: string};

const k = (s: string) => <span style={{color: '#8fb8f0'}}>{s}</span>;
const v = (s: string) => <span style={{color: '#f6c177'}}>{s}</span>;
const g = (s: string) => <span style={{color: '#a5d6a7'}}>{s}</span>;
const m = (s: string) => <span style={{color: c.muted}}>{s}</span>;

const STAGES: Stage[] = [
  {
    name: 'Authenticate',
    tag: 'auth',
    text: 'Clients send a Tollgate key. Tollgate keeps only its SHA-256 hash. Clients never receive the provider key.',
    title: 'terminal',
    code: (
      <>
        {m('$')} tollgate keys create -id laptop
        <br />
        {k('key:  ')}
        {g('tg_7KqZ…')}
        <br />
        {k('hash: ')}
        {v('sha256:4c1e9a…')}
      </>
    ),
  },
  {
    name: 'Preflight',
    tag: 'preflight',
    text: 'Reads the body, checks the model, estimates the tokens, and masks personal data before the provider sees it.',
    title: 'request body',
    code: (
      <>
        {g('"ana@corp.com"')} {m('→')} {v('"[EMAIL_1]"')}
        <br />
        {g('"EMP-204981"  ')} {m('→')} {v('"[EMPLOYEE_ID_1]"')}
      </>
    ),
  },
  {
    name: 'Limit check',
    tag: 'limit check',
    text: 'Sliding-window rate limits and a budget reservation, in one atomic Lua script when copies share Redis.',
    title: 'tollgate.yaml',
    code: (
      <>
        {k('limits:')}
        <br />
        {k('  requests_per_minute: ')}
        {v('120')}
        <br />
        {k('  tokens_per_minute:   ')}
        {v('2000000')}
      </>
    ),
  },
  {
    name: 'Response cache',
    tag: 'cache',
    text: 'An opt-in exact cache, keyed by tenant and request body. A hit costs zero provider tokens.',
    title: 'response headers',
    code: (
      <>
        {k('x-tollgate-cache: ')}
        {g('hit')}
        <br />
        {m('# no upstream attempt, 0 provider tokens')}
      </>
    ),
  },
  {
    name: 'Forward',
    tag: 'chat <model>',
    text: 'Ordered targets with retries, circuit breakers and sticky sessions. Streams retain the provider format.',
    title: 'tollgate.yaml',
    code: (
      <>
        {k('targets:')}
        <br />
        {k('  - provider: ')}
        {g('anthropic')}
        <br />
        {k('  - provider: ')}
        {g('vertex')}
      </>
    ),
  },
  {
    name: 'Settle',
    text: 'The real token usage replaces the reservation. Each request writes one usage event with its exact cost.',
    title: 'usage.jsonl',
    code: (
      <>
        {'{'}
        {k('"provider"')}:{g('"fake-primary"')},{k('"attempts"')}:{v('1')},
        <br />
        {' '}
        {k('"cache_write_tokens"')}:{v('2427')},{k('"ttft_ms"')}:{v('352')},
        <br />
        {' '}
        {k('"cost_micro_usd"')}:{v('6170')}
        {'}'}
      </>
    ),
  },
];

const START = 40;
const STEP = 140;
const ROW_H = 96;
const TOP = 300;

export const Pipeline: React.FC = () => {
  const frame = useCurrentFrame();
  const active = Math.max(0, Math.min(STAGES.length - 1, Math.floor((frame - START) / STEP)));
  const progress = interpolate(frame, [START - 10, START + STEP * (STAGES.length - 1) + 10], [0, 1], {...clamp});
  const done = frame > START + STEP * STAGES.length - 6;
  const foot = interpolate(frame, [START + STEP * STAGES.length - 4, START + STEP * STAGES.length + 20], [0, 1], {...clamp, easing: ease});

  return (
    <AbsoluteFill>
      <Background />
      <SceneTitle n="02" label="Inside a request" title="One request. Six control stages." highlight={{control: c.blueLight}} />

      {/* Rail with a moving request. */}
      <div style={{position: 'absolute', left: 150, top: TOP + ROW_H / 2, width: 4, height: ROW_H * (STAGES.length - 1), background: 'rgba(255,255,255,0.08)', borderRadius: 2}}>
        <div style={{width: 4, height: `${progress * 100}%`, background: c.blueLight, borderRadius: 2, boxShadow: `0 0 16px ${c.blue}`}} />
      </div>

      {STAGES.map((s, i) => {
        const appear = interpolate(frame - 6 - i * 4, [0, 18], [0, 1], {...clamp, easing: ease});
        const on = frame >= START + i * STEP - 8;
        const current = !done && i === active && frame >= START - 8;
        return (
          <div key={s.name} style={{position: 'absolute', left: 120, top: TOP + i * ROW_H, height: ROW_H, display: 'flex', alignItems: 'center', gap: 34, opacity: appear, transform: `translateX(${(1 - appear) * -30}px)`}}>
            <div
              style={{
                width: 64,
                height: 64,
                borderRadius: 32,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontFamily: monoFont,
                fontSize: 26,
                fontWeight: 700,
                color: on ? '#fff' : c.muted,
                background: on ? c.blue : '#161615',
                border: `2px solid ${on ? c.blueLight : 'rgba(255,255,255,0.12)'}`,
                boxShadow: current ? `0 0 0 8px ${c.blue}33, 0 0 30px ${c.blue}` : 'none',
              }}
            >
              {i + 1}
            </div>
            <div>
              <div style={{fontFamily: font, fontSize: 38, fontWeight: 700, letterSpacing: '-0.02em', color: on ? c.text : '#6b6a66'}}>{s.name}</div>
            </div>
          </div>
        );
      })}

      {/* Detail panel of the current stage. */}
      <div style={{position: 'absolute', left: 700, top: TOP + 4, width: 1100, height: 560}}>
        {STAGES.map((s, i) => {
          const t0 = START + i * STEP;
          const inP = interpolate(frame - t0 + 8, [0, 16], [0, 1], {...clamp, easing: ease});
          const outP = i === STAGES.length - 1 ? 0 : interpolate(frame - (t0 + STEP) + 8, [0, 12], [0, 1], {...clamp, easing: ease});
          const op = inP * (1 - outP);
          if (op <= 0) return null;
          return (
            <div key={s.name} style={{position: 'absolute', inset: 0, opacity: op, transform: `translateY(${(1 - inP) * 30 - outP * 20}px)`}}>
              <Card style={{padding: '36px 44px', height: 520, boxSizing: 'border-box'}}>
                <div style={{display: 'flex', alignItems: 'center', gap: 18}}>
                  <span style={{fontFamily: monoFont, fontSize: 28, color: c.blueLight}}>{String(i + 1).padStart(2, '0')}</span>
                  <span style={{fontFamily: font, fontSize: 48, fontWeight: 800, color: c.text, letterSpacing: '-0.03em'}}>{s.name}</span>
                  {s.tag && <span style={{marginLeft: 'auto', fontFamily: monoFont, fontSize: 24, color: c.muted, padding: '6px 14px', borderRadius: 10, border: `1px solid ${c.border}`}}>trace span: {s.tag}</span>}
                </div>
                <div style={{fontFamily: font, fontSize: 34, lineHeight: 1.4, color: '#cfcec9', marginTop: 20}}>{s.text}</div>
                <Terminal title={s.title} style={{marginTop: 30, boxShadow: 'none', background: '#0f0f0e'}}>
                  <div style={{whiteSpace: 'pre', fontSize: 28}}>{s.code}</div>
                </Terminal>
              </Card>
            </div>
          );
        })}
      </div>

      <div style={{position: 'absolute', left: 700, top: TOP + 560, width: 1100, display: 'flex', alignItems: 'center', gap: 20, opacity: foot, transform: `translateY(${(1 - foot) * 16}px)`}}>
        <span style={{fontFamily: monoFont, fontSize: 30, color: c.muted}}>x-tollgate-overhead-ms:</span>
        <span style={{fontFamily: monoFont, fontSize: 34, fontWeight: 700, color: c.good}}>0.089</span>
        <span style={{fontFamily: font, fontSize: 30, color: c.muted}}>— one demo request</span>
      </div>
    </AbsoluteFill>
  );
};
