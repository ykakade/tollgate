import React from 'react';
import {AbsoluteFill, Img, interpolate, staticFile, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, easeInOut, Eyebrow, useSpring, Words} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Screenshots of the real dashboard ("make dashboard-demo"), 1540 CSS px wide at 2x.
const W = 1440; // shown width of the browser viewport
const K = W / 1540;
const VIEW_H = 760;
const SWITCH = 236; // frame where the overview changes to the session page

export const Dashboard: React.FC = () => {
  const frame = useCurrentFrame();
  const intro = interpolate(frame, [0, 70], [0, 1], {...clamp, easing: ease});
  const rx = 26 * (1 - intro);
  const ry = -16 * (1 - intro);
  const ty = 220 * (1 - intro);

  // Camera inside the viewport, in viewport px: zoom to the fallback card, back out, then into the session page.
  const z1 = interpolate(frame, [96, 150, SWITCH - 34, SWITCH - 4], [0, 1, 1, 0], {...clamp, easing: easeInOut});
  const fade = interpolate(frame, [SWITCH - 14, SWITCH + 10], [0, 1], clamp);
  const z2 = interpolate(frame, [SWITCH + 16, 410], [0, 1], {...clamp, easing: easeInOut});
  const cam1 = camera(z1, FOCUS1);
  const cam2 = camera(z2, FOCUS2);

  return (
    <AbsoluteFill>
      <Background intensity={1.1} />
      <AbsoluteFill style={{padding: '78px 120px'}}>
        <Eyebrow n="03" text="Dashboard" />
        <div style={{marginTop: 18}}>
          <Words text="Every dollar. Every fallback. Built in." size={68} weight={700} delay={6} highlight={{dollar: c.blueLight, fallback: c.blueLight}} />
        </div>
      </AbsoluteFill>

      <AbsoluteFill style={{perspective: 2200}}>
        <div
          style={{
            position: 'absolute',
            left: (1920 - W) / 2,
            top: 262,
            width: W,
            transform: `translateY(${ty}px) rotateX(${rx}deg) rotateY(${ry}deg)`,
            transformOrigin: '50% 0%',
            opacity: intro,
          }}
        >
          <Browser url={frame < SWITCH ? '127.0.0.1:9090' : '127.0.0.1:9090/#/sessions/platform/live-0'}>
            <div style={{position: 'relative', width: W, height: VIEW_H, overflow: 'hidden', background: c.page}}>
              <Shot src="overview.png" opacity={1 - fade} transform={cam1}>
                <Highlight show={z1} />
              </Shot>
              <Shot src="session.png" opacity={fade} transform={cam2} />
            </div>
          </Browser>
        </div>
      </AbsoluteFill>

      <Callout x={1180} y={236} delay={150} out={SWITCH - 20} text="Fallbacks and target switches" />
      <Callout x={90} y={930} delay={270} text="Spend and budget for each agent session" />
    </AbsoluteFill>
  );
};

type Focus = {x: number; y: number; s: number};
// Fallback card of the overview, and the KPI row and chart of the session page.
const FOCUS1: Focus = {x: 913 * K, y: 230 * K, s: 1.6};
const FOCUS2: Focus = {x: 770 * K, y: 420 * K, s: 1.22};

// Moves the camera from the full page (t = 0) to the focus point (t = 1).
const camera = (t: number, f: Focus) => {
  const s = 1 + (f.s - 1) * t;
  const cx = W / 2 + (f.x - W / 2) * t;
  const cy = VIEW_H / 2 + (f.y - VIEW_H / 2) * t;
  return `translate(${W / 2 - cx * s}px, ${VIEW_H / 2 - cy * s}px) scale(${s})`;
};

const Shot: React.FC<{src: string; opacity: number; transform: string; children?: React.ReactNode}> = ({src, opacity, transform, children}) => (
  <div style={{position: 'absolute', left: 0, top: 0, width: W, opacity, transform, transformOrigin: '0 0'}}>
    <Img src={staticFile(src)} style={{width: W, display: 'block'}} />
    {children}
  </div>
);

const Highlight: React.FC<{show: number}> = ({show}) => (
  // "Requests with a fallback" card: CSS px 828..999 x 142..266 in the screenshot.
  <div
    style={{
      position: 'absolute',
      left: 828 * K - 5,
      top: 142 * K - 5,
      width: (999 - 828) * K + 10,
      height: (266 - 142) * K + 10,
      borderRadius: 12,
      border: `2px solid ${c.blueLight}`,
      boxShadow: `0 0 30px ${c.blue}, inset 0 0 20px ${c.blue}55`,
      opacity: interpolate(show, [0.8, 1], [0, 1], clamp),
    }}
  />
);

const Browser: React.FC<{url: string; children: React.ReactNode}> = ({url, children}) => (
  <div style={{borderRadius: 18, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.14)', boxShadow: '0 60px 140px rgba(0,0,0,0.7), 0 0 120px rgba(42,120,214,0.18)'}}>
    <div style={{height: 52, display: 'flex', alignItems: 'center', gap: 10, padding: '0 20px', background: '#1b1b1a', borderBottom: '1px solid rgba(255,255,255,0.08)'}}>
      {['#ff5f57', '#febc2e', '#28c840'].map((col) => (
        <div key={col} style={{width: 13, height: 13, borderRadius: 7, background: col}} />
      ))}
      <div style={{marginLeft: 24, flex: 1, maxWidth: 640, height: 32, borderRadius: 8, background: '#0f0f0e', display: 'flex', alignItems: 'center', padding: '0 14px', fontFamily: monoFont, fontSize: 17, color: c.muted}}>
        {url}
      </div>
    </div>
    {children}
  </div>
);

const Callout: React.FC<{x: number; y: number; delay: number; out?: number; text: string}> = ({x, y, delay, out = 1e9, text}) => {
  const frame = useCurrentFrame();
  const p = useSpring(delay, {damping: 12}) * interpolate(frame, [out, out + 12], [1, 0], clamp);
  const float = Math.sin((frame - delay) / 22) * 6;
  return (
    <div
      style={{
        position: 'absolute',
        left: x,
        top: y + float,
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '14px 24px',
        borderRadius: 14,
        background: 'rgba(20,24,32,0.85)',
        border: `1px solid ${c.blue}88`,
        boxShadow: `0 20px 50px rgba(0,0,0,0.6), 0 0 30px ${c.blue}33`,
        fontFamily: font,
        fontWeight: 600,
        fontSize: 26,
        color: c.text,
        whiteSpace: 'nowrap',
        opacity: p,
        transform: `scale(${0.7 + 0.3 * p})`,
        backdropFilter: 'blur(8px)',
      }}
    >
      <span style={{width: 10, height: 10, borderRadius: 5, background: c.blueLight, boxShadow: `0 0 12px ${c.blueLight}`}} />
      {text}
    </div>
  );
};
