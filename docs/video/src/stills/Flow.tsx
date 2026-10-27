import React from 'react';
import {AbsoluteFill} from 'remotion';
import {Logo} from '../components/ui';
import {font, monoFont} from '../theme';

// The "How it works" diagram of the README, in a light and a dark variant.
// The background is transparent, so the cards sit on the GitHub page.
// GitHub shows the image at about 60% of its CSS width, so the smallest
// text is 20 px here. The README text explains each stage.

export type FlowTheme = 'light' | 'dark';

const palette = {
  light: {
    card: '#ffffff',
    panel: '#f6f8fb',
    border: 'rgba(11,11,11,0.14)',
    text: '#0b0b0b',
    muted: '#6d6b66',
    line: '#c3c2b7',
    blue: '#2a78d6',
    blueSoft: 'rgba(42,120,214,0.10)',
    blueText: '#1f63bd',
    red: '#b42f2f',
    redSoft: 'rgba(208,59,59,0.10)',
    amber: '#8a5a00',
    amberSoft: 'rgba(250,178,25,0.18)',
    green: '#006300',
    greenSoft: 'rgba(12,163,12,0.10)',
  },
  dark: {
    card: '#161b22',
    panel: '#111826',
    border: 'rgba(255,255,255,0.14)',
    text: '#f0efec',
    muted: '#a3a198',
    line: '#4a515a',
    blue: '#3987e5',
    blueSoft: 'rgba(57,135,229,0.16)',
    blueText: '#6da7ec',
    red: '#e66767',
    redSoft: 'rgba(229,72,77,0.16)',
    amber: '#fab219',
    amberSoft: 'rgba(250,178,25,0.14)',
    green: '#3fb950',
    greenSoft: 'rgba(63,185,80,0.14)',
  },
};

type P = (typeof palette)['light'];
type Tone = 'red' | 'amber' | 'green' | 'blue';

export const FLOW_W = 1400;
export const FLOW_H = 632;

const clients = [
  ['Claude Code', 'ANTHROPIC_BASE_URL'],
  ['Anthropic SDK', '/v1/messages'],
  ['OpenAI SDK', '/v1/chat/completions'],
  ['Your own agent', 'any base URL'],
] as const;

const providers: [string, string, [string, Tone] | null][] = [
  ['Anthropic', 'Messages API', ['primary', 'blue']],
  ['Vertex AI', 'Claude on Google Cloud', ['fallback', 'amber']],
  ['OpenAI', 'Chat Completions', null],
  ['Ollama', 'local models', ['local', 'green']],
];

const stages: [string, [string, Tone] | null][] = [
  ['Authenticate', null],
  ['Preflight', null],
  ['Limit check', ['429 · 402', 'red']],
  ['Response cache', ['hit: 0 tokens', 'green']],
  ['Forward', ['529 → backup', 'amber']],
  ['Settle', ['cost per request', 'blue']],
];

const outputs: [string, number][] = [
  ['Dashboard', 156],
  ['JSONL usage', 170],
  ['Prometheus', 164],
  ['OpenTelemetry', 196],
];

// Geometry.
const margin = 20;
const sideW = 320;
const gap = 70;
const panelX = margin + sideW + gap;
const panelW = FLOW_W - 2 * (margin + sideW + gap);
const panelY = 24;
const headH = 96;
const stageH = 60;
const panelH = headH + stages.length * stageH + 18;
const midY = panelY + panelH / 2;
const cardH = 98;
const cardGap = (panelH - 4 * cardH) / 3;
const cardY = (i: number) => panelY + i * (cardH + cardGap);
const rightX = FLOW_W - margin - sideW;
const busY = panelY + panelH + 34;
const outY = busY + 22;
const outGap = 14;
const outTotal = outputs.reduce((s, [, w]) => s + w, 0) + outGap * (outputs.length - 1);
const outX0 = panelX + panelW / 2 - outTotal / 2;
const outX = (i: number) => outX0 + outputs.slice(0, i).reduce((s, [, w]) => s + w + outGap, 0);

const tone = (p: P, t: Tone) =>
  ({red: [p.red, p.redSoft], amber: [p.amber, p.amberSoft], green: [p.green, p.greenSoft], blue: [p.blueText, p.blueSoft]})[t];

const Tag: React.FC<{p: P; t: [string, Tone]}> = ({p, t}) => {
  const [fg, bg] = tone(p, t[1]);
  return <span style={{fontFamily: monoFont, fontSize: 20, padding: '5px 12px', borderRadius: 999, color: fg, background: bg, whiteSpace: 'nowrap'}}>{t[0]}</span>;
};

const Card: React.FC<{p: P; x: number; y: number; title: string; sub: string; tag?: [string, Tone] | null}> = ({p, x, y, title, sub, tag}) => (
  <div
    style={{
      position: 'absolute',
      left: x,
      top: y,
      width: sideW,
      height: cardH,
      boxSizing: 'border-box',
      padding: '0 22px',
      borderRadius: 16,
      background: p.card,
      border: `2px solid ${p.border}`,
      display: 'flex',
      flexDirection: 'column',
      justifyContent: 'center',
      gap: 6,
    }}
  >
    <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10}}>
      <span style={{fontFamily: font, fontSize: 30, fontWeight: 700, letterSpacing: '-0.02em', color: p.text, whiteSpace: 'nowrap'}}>{title}</span>
      {tag ? <Tag p={p} t={tag} /> : null}
    </div>
    <span style={{fontFamily: monoFont, fontSize: 20, color: p.muted, whiteSpace: 'nowrap'}}>{sub}</span>
  </div>
);

export const Flow: React.FC<{theme: FlowTheme}> = ({theme}) => {
  const p = palette[theme];
  const curve = (x1: number, y1: number, x2: number, y2: number) => {
    const dx = (x2 - x1) * 0.55;
    return `M${x1} ${y1} C${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
  };

  return (
    <AbsoluteFill style={{fontFamily: font}}>
      <svg width={FLOW_W} height={FLOW_H} style={{position: 'absolute', inset: 0}}>
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0 0 L10 5 L0 10 z" fill={p.blue} />
          </marker>
          <marker id="arrow-muted" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0 0 L10 5 L0 10 z" fill={p.line} />
          </marker>
        </defs>
        {clients.map((_, i) => (
          <path key={i} d={curve(margin + sideW, cardY(i) + cardH / 2, panelX - 6, midY)} stroke={p.blue} strokeWidth={3} fill="none" opacity={0.85} markerEnd={i === 1 ? 'url(#arrow)' : undefined} />
        ))}
        {providers.map((_, i) => (
          <path
            key={i}
            d={curve(panelX + panelW, midY, rightX - 6, cardY(i) + cardH / 2)}
            stroke={i < 2 ? p.blue : p.line}
            strokeWidth={i < 2 ? 3 : 2.5}
            strokeDasharray={i === 1 ? '10 8' : undefined}
            fill="none"
            markerEnd={i < 2 ? 'url(#arrow)' : 'url(#arrow-muted)'}
          />
        ))}
        <path d={`M${panelX + panelW / 2} ${panelY + panelH} V${busY}`} stroke={p.line} strokeWidth={2.5} fill="none" />
        <path d={`M${outX(0) + outputs[0][1] / 2} ${busY} H${outX(outputs.length - 1) + outputs[outputs.length - 1][1] / 2}`} stroke={p.line} strokeWidth={2.5} fill="none" />
        {outputs.map(([, w], i) => (
          <path key={i} d={`M${outX(i) + w / 2} ${busY} V${outY - 3}`} stroke={p.line} strokeWidth={2.5} fill="none" markerEnd="url(#arrow-muted)" />
        ))}
      </svg>

      {clients.map(([t, s], i) => (
        <Card key={t} p={p} x={margin} y={cardY(i)} title={t} sub={s} />
      ))}
      {providers.map(([t, s, tag], i) => (
        <Card key={t} p={p} x={rightX} y={cardY(i)} title={t} sub={s} tag={tag} />
      ))}

      <div
        style={{
          position: 'absolute',
          left: panelX,
          top: panelY,
          width: panelW,
          height: panelH,
          boxSizing: 'border-box',
          borderRadius: 24,
          background: p.panel,
          border: `2.5px solid ${p.blue}`,
          boxShadow: theme === 'dark' ? `0 0 60px ${p.blueSoft}` : '0 18px 50px rgba(42,120,214,0.12)',
        }}
      >
        <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', height: headH, padding: '0 30px'}}>
          <div style={{display: 'flex', alignItems: 'center', gap: 14}}>
            <Logo size={48} />
            <span style={{fontSize: 38, fontWeight: 800, letterSpacing: '-0.035em', color: p.text}}>Tollgate</span>
          </div>
          <span style={{fontFamily: monoFont, fontSize: 20, color: p.muted}}>one Go binary</span>
        </div>
        {stages.map(([name, tag], i) => (
          <div key={name} style={{position: 'absolute', left: 30, right: 30, top: headH + i * stageH, height: stageH, display: 'flex', alignItems: 'center', gap: 18}}>
            <div
              style={{
                width: 42,
                height: 42,
                borderRadius: '50%',
                background: p.blue,
                color: '#fff',
                fontFamily: monoFont,
                fontSize: 21,
                fontWeight: 700,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
              }}
            >
              {i + 1}
            </div>
            <span style={{flex: 1, fontSize: 29, fontWeight: 700, letterSpacing: '-0.015em', color: p.text}}>{name}</span>
            {tag ? <Tag p={p} t={tag} /> : null}
          </div>
        ))}
      </div>

      {outputs.map(([o, w], i) => (
        <div
          key={o}
          style={{
            position: 'absolute',
            left: outX(i),
            top: outY,
            width: w,
            height: 56,
            boxSizing: 'border-box',
            borderRadius: 12,
            background: p.card,
            border: `2px solid ${p.border}`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 22,
            fontWeight: 600,
            color: p.text,
          }}
        >
          {o}
        </div>
      ))}
    </AbsoluteFill>
  );
};
