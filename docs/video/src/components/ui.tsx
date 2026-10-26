import React from 'react';
import {AbsoluteFill, getInputProps, interpolate, random, spring, useCurrentFrame, useVideoConfig, Easing} from 'remotion';
import {c, font, monoFont} from '../theme';

export const ease = Easing.bezier(0.16, 1, 0.3, 1); // expo-out
export const easeInOut = Easing.bezier(0.65, 0, 0.35, 1);

export const clamp = {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'} as const;

// Spring that starts at `delay` frames.
export const useSpring = (delay = 0, config: {damping?: number; stiffness?: number; mass?: number} = {}) => {
  const frame = useCurrentFrame();
  const {fps} = useVideoConfig();
  return spring({frame: frame - delay, fps, config: {damping: 18, stiffness: 140, mass: 0.9, ...config}});
};

// Background: dot grid, two slow glows, vignette and film grain.
export const Background: React.FC<{glow?: string; glow2?: string; intensity?: number}> = ({
  glow = c.blue,
  glow2 = '#5b3fd6',
  intensity = 1,
}) => {
  const frame = useCurrentFrame();
  // With the "still" prop (for the README WebP), the background does not move, so that unchanged pixels compress.
  const t = getInputProps().still === true ? 0 : frame / 60;
  const x1 = 30 + Math.sin(t * 0.35) * 8;
  const y1 = 35 + Math.cos(t * 0.3) * 6;
  const x2 = 72 + Math.cos(t * 0.27) * 8;
  const y2 = 70 + Math.sin(t * 0.33) * 6;
  return (
    <AbsoluteFill style={{backgroundColor: c.bg, overflow: 'hidden'}}>
      <AbsoluteFill
        style={{
          backgroundImage: 'radial-gradient(rgba(255,255,255,0.09) 1.4px, transparent 1.6px)',
          backgroundSize: '44px 44px',
          backgroundPosition: `${-t * 6}px ${-t * 4}px`,
          maskImage: 'radial-gradient(ellipse 75% 70% at 50% 50%, black 30%, transparent 100%)',
          WebkitMaskImage: 'radial-gradient(ellipse 75% 70% at 50% 50%, black 30%, transparent 100%)',
        }}
      />
      <AbsoluteFill style={{background: `radial-gradient(circle at ${x1}% ${y1}%, ${glow} 0%, transparent 38%)`, opacity: 0.25 * intensity, filter: 'blur(20px)'}} />
      <AbsoluteFill style={{background: `radial-gradient(circle at ${x2}% ${y2}%, ${glow2} 0%, transparent 36%)`, opacity: 0.19 * intensity, filter: 'blur(20px)'}} />
      <AbsoluteFill style={{background: 'radial-gradient(ellipse 85% 80% at 50% 50%, transparent 55%, rgba(0,0,0,0.75) 100%)'}} />
      <Grain />
    </AbsoluteFill>
  );
};

// Film grain. The "still" render has none, because noise makes the file large.
export const Grain: React.FC = () => {
  const frame = useCurrentFrame();
  if (getInputProps().still === true) return null;
  const seed = Math.floor(frame / 2) % 50;
  return (
    <AbsoluteFill style={{opacity: 0.07, mixBlendMode: 'overlay', pointerEvents: 'none'}}>
      <svg width="100%" height="100%">
        <filter id={`grain-${seed}`}>
          <feTurbulence type="fractalNoise" baseFrequency="0.85" numOctaves="3" seed={seed} stitchTiles="stitch" />
        </filter>
        <rect width="100%" height="100%" filter={`url(#grain-${seed})`} />
      </svg>
    </AbsoluteFill>
  );
};

// The Tollgate mark from web/public/favicon.svg. `draw` reveals the T.
export const Logo: React.FC<{size: number; draw?: number; glow?: number}> = ({size, draw = 1, glow = 0}) => {
  const opacity = interpolate(draw, [0, 1], [0, 1], clamp);
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      style={{filter: glow > 0 ? `drop-shadow(0 0 ${size * 0.25 * glow}px rgba(42,120,214,${0.8 * glow}))` : undefined, overflow: 'visible'}}
    >
      <rect width="32" height="32" rx="7" fill="#2a78d6" />
      <path d="M7 7h18v6h-6v12h-6V13H7Z" fill="#fff" opacity={opacity} />
    </svg>
  );
};

// Small uppercase section label, for example "01 · Failover".
export const Eyebrow: React.FC<{n: string; text: string; delay?: number; color?: string}> = ({n, text, delay = 0, color = c.blueLight}) => {
  const p = useSpring(delay);
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 14,
        fontFamily: monoFont,
        fontSize: 22,
        letterSpacing: '0.18em',
        textTransform: 'uppercase',
        color: c.muted,
        opacity: p,
        transform: `translateY(${(1 - p) * 16}px)`,
      }}
    >
      <span style={{color}}>{n}</span>
      <span style={{width: 40 * p, height: 2, background: color, display: 'inline-block'}} />
      <span>{text}</span>
    </div>
  );
};

// Headline whose words rise in one after the other.
export const Words: React.FC<{
  text: string;
  delay?: number;
  stagger?: number;
  size?: number;
  weight?: number;
  color?: string;
  highlight?: Record<string, string>;
  style?: React.CSSProperties;
}> = ({text, delay = 0, stagger = 4, size = 84, weight = 700, color = c.text, highlight = {}, style}) => {
  const frame = useCurrentFrame();
  const words = text.split(' ');
  return (
    <div style={{fontFamily: font, fontSize: size, fontWeight: weight, letterSpacing: '-0.035em', lineHeight: 1.08, color, ...style}}>
      {words.map((w, i) => {
        const p = interpolate(frame - delay - i * stagger, [0, 22], [0, 1], {...clamp, easing: ease});
        const hl = highlight[w.replace(/[.,]/g, '')];
        return (
          <span key={i} style={{display: 'inline-block', overflow: 'visible', marginRight: '0.24em'}}>
            <span
              style={{
                display: 'inline-block',
                opacity: p,
                transform: `translateY(${(1 - p) * 0.5}em)`,
                filter: `blur(${(1 - p) * 10}px)`,
                ...(hl?.startsWith('linear-gradient')
                  ? {backgroundImage: hl, WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent', paddingBottom: '0.08em'}
                  : {color: hl ?? undefined}),
              }}
            >
              {w}
            </span>
          </span>
        );
      })}
    </div>
  );
};

// Text that appears character by character.
export const typed = (text: string, frame: number, start: number, cps = 60, fps = 60) => {
  const n = Math.max(0, Math.floor(((frame - start) / fps) * cps));
  return text.slice(0, n);
};

export const Cursor: React.FC<{color?: string}> = ({color = c.text}) => {
  const frame = useCurrentFrame();
  return <span style={{display: 'inline-block', width: '0.55em', height: '1.1em', marginLeft: 2, verticalAlign: '-0.2em', background: color, opacity: Math.floor(frame / 18) % 2 === 0 ? 0.9 : 0}} />;
};

export const Card: React.FC<{style?: React.CSSProperties; children?: React.ReactNode; glow?: string}> = ({style, children, glow}) => (
  <div
    style={{
      background: 'linear-gradient(180deg, rgba(34,34,32,0.92) 0%, rgba(22,22,21,0.92) 100%)',
      border: `1px solid ${c.border}`,
      borderRadius: 22,
      boxShadow: `0 30px 80px rgba(0,0,0,0.55), inset 0 1px 0 rgba(255,255,255,0.06)${glow ? `, 0 0 60px ${glow}` : ''}`,
      ...style,
    }}
  >
    {children}
  </div>
);

// A terminal window with a title bar.
export const Terminal: React.FC<{title?: string; style?: React.CSSProperties; children?: React.ReactNode}> = ({title = 'bash', style, children}) => (
  <Card style={{overflow: 'hidden', ...style}}>
    <div style={{display: 'flex', alignItems: 'center', gap: 10, padding: '16px 20px', borderBottom: `1px solid ${c.border}`, background: 'rgba(255,255,255,0.02)'}}>
      {['#ff5f57', '#febc2e', '#28c840'].map((col) => (
        <div key={col} style={{width: 14, height: 14, borderRadius: 7, background: col, opacity: 0.9}} />
      ))}
      <div style={{flex: 1, textAlign: 'center', fontFamily: monoFont, fontSize: 18, color: c.muted, marginRight: 62}}>{title}</div>
    </div>
    <div style={{padding: '22px 28px', fontFamily: monoFont, fontSize: 24, lineHeight: 1.6, color: c.text}}>{children}</div>
  </Card>
);

export const Pill: React.FC<{color: string; children: React.ReactNode; style?: React.CSSProperties}> = ({color, children, style}) => (
  <span
    style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: 10,
      padding: '8px 18px',
      borderRadius: 999,
      background: `${color}22`,
      border: `1px solid ${color}66`,
      color,
      fontFamily: monoFont,
      fontWeight: 500,
      ...style,
    }}
  >
    {children}
  </span>
);

// Slow push-in so that no frame is fully static.
export const Drift: React.FC<{children: React.ReactNode; dur: number; from?: number; to?: number}> = ({children, dur, from = 1, to = 1.04}) => {
  const frame = useCurrentFrame();
  const s = interpolate(frame, [0, dur], [from, to], clamp);
  return <AbsoluteFill style={{transform: `scale(${s})`}}>{children}</AbsoluteFill>;
};

export const shake = (frame: number, start: number, len = 14, amp = 14) => {
  const k = interpolate(frame - start, [0, len], [1, 0], clamp);
  if (k <= 0 || frame < start) return {x: 0, y: 0};
  return {x: (random(`sx${frame}`) - 0.5) * 2 * amp * k, y: (random(`sy${frame}`) - 0.5) * 2 * amp * k};
};
