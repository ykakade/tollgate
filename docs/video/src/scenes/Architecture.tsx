import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {getLength, getPointAtLength} from '@remotion/paths';
import {Background, Card, clamp, ease, Logo, SceneTitle, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Clients on the left, Tollgate in the middle, model APIs on the right.
const CLIENTS = ['Claude Code', 'Anthropic SDK', 'OpenAI SDK', 'Your own agent'];
const PROVIDERS: [string, string][] = [
  ['Anthropic', '/v1/messages'],
  ['OpenAI', '/v1/chat/completions'],
  ['Vertex AI', 'Google Cloud'],
  ['Ollama', 'local models'],
];
const CAPS = ['Key auth', 'Rate limits', 'Budgets', 'PII redaction', 'Response cache', 'Failover', 'Metrics and traces'];

const ROW_Y = [420, 545, 670, 795];
const CL = {x: 120, w: 400};
const GATE = {x: 730, y: 330, w: 460, h: 560};
const PR = {x: 1400, w: 400};

const leftPath = (y: number) => `M ${CL.x + CL.w} ${y} C ${CL.x + CL.w + 120} ${y}, ${GATE.x - 120} 610, ${GATE.x} 610`;
const rightPath = (y: number) => `M ${GATE.x + GATE.w} 610 C ${GATE.x + GATE.w + 120} 610, ${PR.x - 120} ${y}, ${PR.x} ${y}`;

export const Architecture: React.FC = () => {
  const frame = useCurrentFrame();
  const foot = interpolate(frame, [190, 215], [0, 1], {...clamp, easing: ease});
  return (
    <AbsoluteFill>
      <Background />
      <SceneTitle n="01" label="What it is" title="One binary in front of every model API." highlight={{binary: c.blueLight}} />

      <svg width={1920} height={1080} style={{position: 'absolute', inset: 0}}>
        {ROW_Y.map((y, i) => (
          <React.Fragment key={i}>
            <Wire d={leftPath(y)} start={30 + i * 4} />
            <Wire d={rightPath(y)} start={44 + i * 4} />
            <Flow d={leftPath(y)} start={70 + i * 9} />
            <Flow d={rightPath(y)} start={100 + i * 9} />
          </React.Fragment>
        ))}
      </svg>

      {CLIENTS.map((t, i) => (
        <Box key={t} x={CL.x} y={ROW_Y[i]} w={CL.w} delay={10 + i * 5}>
          <div style={{fontFamily: font, fontSize: 36, fontWeight: 700, color: c.text, letterSpacing: '-0.02em'}}>{t}</div>
        </Box>
      ))}

      <GateCard />

      {PROVIDERS.map(([t, sub], i) => (
        <Box key={t} x={PR.x} y={ROW_Y[i]} w={PR.w} delay={22 + i * 5}>
          <div style={{fontFamily: font, fontSize: 36, fontWeight: 700, color: c.text, letterSpacing: '-0.02em'}}>{t}</div>
          <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted, marginTop: 4}}>{sub}</div>
        </Box>
      ))}

      <div style={{position: 'absolute', left: 0, right: 0, top: 952, textAlign: 'center', fontFamily: monoFont, fontSize: 30, color: c.muted, opacity: foot, transform: `translateY(${(1 - foot) * 16}px)`}}>
        Native API formats<span style={{color: c.blueLight}}> · </span>Streaming responses<span style={{color: c.blueLight}}> · </span>One gateway URL
      </div>
    </AbsoluteFill>
  );
};

const Box: React.FC<{x: number; y: number; w: number; delay: number; children: React.ReactNode}> = ({x, y, w, delay, children}) => {
  const p = useSpring(delay);
  return (
    <div style={{position: 'absolute', left: x, top: y - 50, width: w, height: 100, opacity: p, transform: `translateY(${(1 - p) * 30}px)`}}>
      <Card style={{height: '100%', boxSizing: 'border-box', padding: '0 30px', display: 'flex', flexDirection: 'column', justifyContent: 'center', borderRadius: 18}}>{children}</Card>
    </div>
  );
};

const GateCard: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(16, {damping: 15});
  return (
    <div style={{position: 'absolute', left: GATE.x, top: GATE.y, width: GATE.w, height: GATE.h, opacity: p, transform: `scale(${0.9 + 0.1 * p})`}}>
      <Card glow={`${c.blue}40`} style={{height: '100%', boxSizing: 'border-box', padding: '30px 34px', borderColor: `${c.blue}aa`}}>
        <div style={{display: 'flex', alignItems: 'center', gap: 18}}>
          <Logo size={60} glow={0.6} />
          <div style={{fontFamily: font, fontSize: 46, fontWeight: 800, color: c.text, letterSpacing: '-0.03em'}}>Tollgate</div>
        </div>
        <div style={{marginTop: 22, display: 'flex', flexDirection: 'column', gap: 9}}>
          {CAPS.map((t, i) => {
            const on = interpolate(frame - 60 - i * 12, [0, 14], [0, 1], {...clamp, easing: ease});
            return (
              <div key={t} style={{display: 'flex', alignItems: 'center', gap: 16, fontFamily: font, fontSize: 32, fontWeight: 600, color: on > 0.5 ? c.text : '#5f5e5a'}}>
                <svg width={30} height={30} viewBox="0 0 24 24" style={{flexShrink: 0}}>
                  <circle cx={12} cy={12} r={11} fill={on > 0.5 ? `${c.good}26` : 'rgba(255,255,255,0.05)'} stroke={on > 0.5 ? c.good : 'rgba(255,255,255,0.15)'} strokeWidth={1.5} />
                  <path d="M7 12.5l3.2 3.2L17 9" fill="none" stroke={c.good} strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round" pathLength={1} strokeDasharray={1} strokeDashoffset={1 - on} />
                </svg>
                {t}
              </div>
            );
          })}
        </div>
      </Card>
    </div>
  );
};

const Wire: React.FC<{d: string; start: number}> = ({d, start}) => {
  const frame = useCurrentFrame();
  const len = getLength(d);
  const p = interpolate(frame - start, [0, 30], [0, 1], {...clamp, easing: ease});
  return <path d={d} stroke="rgba(109,167,236,0.35)" strokeWidth={2.5} fill="none" strokeDasharray={len} strokeDashoffset={len * (1 - p)} />;
};

// Packets that move along a wire in a loop.
const Flow: React.FC<{d: string; start: number}> = ({d, start}) => {
  const frame = useCurrentFrame();
  if (frame < start) return null;
  const len = getLength(d);
  const period = 70;
  const dots = [0, 0.5].map((off, i) => {
    const t = (((frame - start) / period + off) % 1 + 1) % 1;
    if (frame - start < off * period) return null;
    const pt = getPointAtLength(d, t * len)!;
    return (
      <g key={i}>
        <circle cx={pt.x} cy={pt.y} r={14} fill={c.blueLight} opacity={0.18} />
        <circle cx={pt.x} cy={pt.y} r={6} fill="#fff" />
      </g>
    );
  });
  return <g>{dots}</g>;
};
