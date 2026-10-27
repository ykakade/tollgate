import React from 'react';
import {AbsoluteFill, Img, staticFile} from 'remotion';
import {Logo} from '../components/ui';
import {c, font, monoFont} from '../theme';

// The README banner and the GitHub social preview. The artwork in
// public/readme/banner-art.jpg has no text; this file adds the mark and the
// words, so that they stay sharp and match the logo.

export const BANNER_W = 1600;
export const BANNER_H = 520;
export const SOCIAL_W = 1280;
export const SOCIAL_H = 640;

const art = staticFile('readme/banner-art.jpg');

const facts = [
  ['Hard budgets', 'per session'],
  ['Failover', 'before the first byte'],
  ['A price', 'on every request'],
] as const;

const Facts: React.FC<{size: number; count?: number}> = ({size, count = facts.length}) => (
  <div style={{display: 'flex', flexWrap: 'wrap', gap: size * 0.55}}>
    {facts.slice(0, count).map(([a, b]) => (
      <div
        key={a}
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: size * 0.45,
          padding: `${size * 0.5}px ${size * 0.8}px`,
          borderRadius: size * 0.55,
          border: `1.5px solid ${c.blue}88`,
          background: 'rgba(14,32,58,0.72)',
          backdropFilter: 'blur(6px)',
        }}
      >
        <span style={{fontFamily: font, fontWeight: 800, fontSize: size, color: c.text, letterSpacing: '-0.02em'}}>{a}</span>
        <span style={{fontFamily: monoFont, fontSize: size * 0.8, color: c.blueLight}}>{b}</span>
      </div>
    ))}
  </div>
);

const Wordmark: React.FC<{size: number}> = ({size}) => (
  <div style={{display: 'flex', alignItems: 'center', gap: size * 0.32}}>
    <Logo size={size * 0.86} glow={0.7} />
    <span style={{fontFamily: font, fontSize: size, fontWeight: 800, letterSpacing: '-0.05em', color: c.text, lineHeight: 1}}>Tollgate</span>
  </div>
);

const Tagline: React.FC<{size: number}> = ({size}) => (
  <div style={{fontFamily: font, fontSize: size, fontWeight: 600, color: '#d6d5d0', letterSpacing: '-0.025em', lineHeight: 1.15}}>
    The AI gateway for{' '}
    <span style={{fontWeight: 800, backgroundImage: `linear-gradient(100deg, #ffffff 10%, ${c.blueLight} 90%)`, WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent'}}>
      coding agents
    </span>
    .
  </div>
);

// Artwork on the right, faded into the page colour on the left.
const Art: React.FC<{width: string; fade: string}> = ({width, fade}) => (
  <>
    <div style={{position: 'absolute', top: 0, right: 0, bottom: 0, width, overflow: 'hidden'}}>
      <Img src={art} style={{width: '100%', height: '100%', objectFit: 'cover', objectPosition: '50% 50%'}} />
    </div>
    <AbsoluteFill style={{background: fade}} />
  </>
);

export const Banner: React.FC = () => (
  <AbsoluteFill style={{borderRadius: 28, overflow: 'hidden', backgroundColor: c.bg}}>
    <Art width="68%" fade={`linear-gradient(90deg, ${c.bg} 0%, ${c.bg} 32%, rgba(8,9,11,0.78) 46%, rgba(8,9,11,0.15) 72%, rgba(8,9,11,0) 100%)`} />
    <AbsoluteFill style={{borderRadius: 28, boxShadow: 'inset 0 0 0 1.5px rgba(255,255,255,0.08)'}} />
    <AbsoluteFill style={{justifyContent: 'center', paddingLeft: 96, gap: 30}}>
      <Wordmark size={112} />
      <Tagline size={50} />
      <div style={{marginTop: 14}}>
        <Facts size={23} />
      </div>
    </AbsoluteFill>
  </AbsoluteFill>
);

export const Social: React.FC = () => (
  <AbsoluteFill style={{backgroundColor: c.bg}}>
    <Art width="78%" fade={`linear-gradient(90deg, ${c.bg} 0%, ${c.bg} 24%, rgba(8,9,11,0.82) 44%, rgba(8,9,11,0.2) 75%, rgba(8,9,11,0) 100%)`} />
    <AbsoluteFill style={{justifyContent: 'center', paddingLeft: 84, paddingRight: 84, gap: 30}}>
      <Wordmark size={112} />
      <Tagline size={48} />
      <div style={{marginTop: 10}}>
        <Facts size={23} count={2} />
      </div>
    </AbsoluteFill>
    <div style={{position: 'absolute', left: 84, bottom: 44, fontFamily: monoFont, fontSize: 22, color: c.muted}}>github.com/ykakade/tollgate</div>
  </AbsoluteFill>
);
