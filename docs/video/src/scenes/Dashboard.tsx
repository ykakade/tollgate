import React from 'react';
import {AbsoluteFill, Img, interpolate, staticFile, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, easeInOut, SceneTitle, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Screenshots of the real dashboard ("make dashboard-demo"), 1540 CSS px wide at 2x.
const W = 1440; // shown width of the browser viewport
const K = W / 1540;
const VIEW_H = 640;
const TOP = 268;

type Shot = {src: string; url: string; from: number; to: number; focus: {x: number; y: number; s: number}; box: [number, number, number, number]; text: string};
// focus and box use CSS px of the screenshot.
const SHOTS: Shot[] = [
  {src: 'overview.png', url: '127.0.0.1:9090/#/', from: 0, to: 230, focus: {x: 770, y: 205, s: 1.22}, box: [174, 142, 1366, 266], text: 'Spend, failures, cache reads and fallbacks'},
  {src: 'session.png', url: '127.0.0.1:9090/#/sessions/platform/live-0', from: 230, to: 450, focus: {x: 770, y: 250, s: 1.22}, box: [174, 192, 1366, 316], text: 'Budget left and time left for each agent session'},
  {src: 'providers.png', url: '127.0.0.1:9090/#/providers', from: 450, to: 700, focus: {x: 770, y: 285, s: 1.15}, box: [174, 142, 1366, 428], text: 'Breaker state and time to first token per target'},
];

export const Dashboard: React.FC = () => {
  const frame = useCurrentFrame();
  const intro = interpolate(frame, [0, 60], [0, 1], {...clamp, easing: ease});
  const current = SHOTS.findIndex((s) => frame < s.to);
  const url = SHOTS[current === -1 ? SHOTS.length - 1 : current].url;

  return (
    <AbsoluteFill>
      <Background intensity={1.1} />
      <SceneTitle n="08" label="Dashboard" title="A dashboard in the same binary." highlight={{dashboard: c.blueLight}} />

      <AbsoluteFill style={{perspective: 2400}}>
        <div
          style={{
            position: 'absolute',
            left: (1920 - W) / 2,
            top: TOP,
            width: W,
            transform: `translateY(${180 * (1 - intro)}px) rotateX(${22 * (1 - intro)}deg)`,
            transformOrigin: '50% 0%',
            opacity: intro,
          }}
        >
          <Browser url={url}>
            <div style={{position: 'relative', width: W, height: VIEW_H, overflow: 'hidden', background: c.page}}>
              {SHOTS.map((s, i) => {
                const local = frame - s.from;
                const fadeIn = i === 0 ? 1 : interpolate(local, [-12, 8], [0, 1], clamp);
                if (frame < s.from - 12 || frame > s.to + 10) return null;
                const z = interpolate(local, [50, 100, s.to - s.from - 40, s.to - s.from - 6], [0, 1, 1, 0], {...clamp, easing: easeInOut});
                return (
                  <div key={s.src} style={{position: 'absolute', inset: 0, opacity: fadeIn}}>
                    <div style={{position: 'absolute', left: 0, top: 0, width: W, transform: camera(z, s.focus), transformOrigin: '0 0'}}>
                      <Img src={staticFile(s.src)} style={{width: W, display: 'block'}} />
                      <div
                        style={{
                          position: 'absolute',
                          left: s.box[0] * K - 6,
                          top: s.box[1] * K - 6,
                          width: (s.box[2] - s.box[0]) * K + 12,
                          height: (s.box[3] - s.box[1]) * K + 12,
                          borderRadius: 14,
                          border: `3px solid ${c.blueLight}`,
                          boxShadow: `0 0 30px ${c.blue}`,
                          opacity: interpolate(z, [0.85, 1], [0, 1], clamp),
                        }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          </Browser>
        </div>
      </AbsoluteFill>

      {SHOTS.map((s) => (
        <Callout key={s.src} from={s.from + 96} to={s.to - 34} text={s.text} />
      ))}
    </AbsoluteFill>
  );
};

// Moves the camera from the full page (t = 0) to the focus point (t = 1).
// The page edges stay inside the viewport, so that no empty band shows.
const camera = (t: number, f: {x: number; y: number; s: number}) => {
  const s = 1 + (f.s - 1) * t;
  const tx = Math.min(0, Math.max(W - W * s, W / 2 - f.x * K * s));
  const ty = Math.min(0, VIEW_H / 2 - f.y * K * s);
  return `translate(${tx}px, ${ty}px) scale(${s})`;
};

const Browser: React.FC<{url: string; children: React.ReactNode}> = ({url, children}) => (
  <div style={{borderRadius: 18, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.14)', boxShadow: '0 60px 140px rgba(0,0,0,0.7), 0 0 120px rgba(42,120,214,0.18)'}}>
    <div style={{height: 56, display: 'flex', alignItems: 'center', gap: 10, padding: '0 22px', background: '#1b1b1a', borderBottom: '1px solid rgba(255,255,255,0.08)'}}>
      {['#ff5f57', '#febc2e', '#28c840'].map((col) => (
        <div key={col} style={{width: 14, height: 14, borderRadius: 7, background: col}} />
      ))}
      <div style={{marginLeft: 24, flex: 1, maxWidth: 760, height: 36, borderRadius: 9, background: '#0f0f0e', display: 'flex', alignItems: 'center', padding: '0 16px', fontFamily: monoFont, fontSize: 22, color: c.muted}}>{url}</div>
    </div>
    {children}
  </div>
);

const Callout: React.FC<{from: number; to: number; text: string}> = ({from, to, text}) => {
  const frame = useCurrentFrame();
  const p = useSpring(from, {damping: 14}) * interpolate(frame, [to, to + 12], [1, 0], clamp);
  if (p <= 0.001) return null;
  return (
    <div style={{position: 'absolute', left: 0, right: 0, top: 880, display: 'flex', justifyContent: 'center'}}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 16,
          padding: '18px 32px',
          borderRadius: 16,
          background: 'rgba(16,20,28,0.94)',
          border: `1px solid ${c.blue}aa`,
          boxShadow: `0 24px 60px rgba(0,0,0,0.7), 0 0 34px ${c.blue}44`,
          fontFamily: font,
          fontWeight: 700,
          fontSize: 36,
          color: c.text,
          whiteSpace: 'nowrap',
          opacity: p,
          transform: `translateY(${(1 - p) * 20}px) scale(${0.9 + 0.1 * p})`,
        }}
      >
        <span style={{width: 12, height: 12, borderRadius: 6, background: c.blueLight, boxShadow: `0 0 12px ${c.blueLight}`}} />
        {text}
      </div>
    </div>
  );
};
