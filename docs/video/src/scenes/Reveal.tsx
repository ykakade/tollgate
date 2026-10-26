import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, Logo, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// The mark draws itself, then the wordmark and the tagline come in.
export const Reveal: React.FC = () => {
  const frame = useCurrentFrame();
  const pop = useSpring(0, {damping: 12, stiffness: 120});
  const draw = interpolate(frame, [8, 48], [0, 1], {...clamp, easing: ease});
  const slide = interpolate(frame, [40, 75], [0, 1], {...clamp, easing: ease});
  const ring = interpolate(frame, [30, 80], [0, 1], clamp);
  const sweep = interpolate(frame, [70, 120], [-30, 130], clamp);
  const word = 'Tollgate';

  return (
    <AbsoluteFill>
      <Background intensity={1.2} />
      {/* Shock ring when the mark lands. */}
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center'}}>
        <div
          style={{
            width: 300,
            height: 300,
            borderRadius: '50%',
            border: `3px solid ${c.blueLight}`,
            opacity: (1 - ring) * 0.6,
            transform: `translateY(-70px) scale(${0.5 + ring * 4})`,
          }}
        />
      </AbsoluteFill>
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center', flexDirection: 'column', gap: 36}}>
        <div style={{display: 'flex', alignItems: 'center', gap: 44 * slide}}>
          <div style={{transform: `scale(${0.4 + pop * 0.6}) rotate(${(1 - pop) * -20}deg)`}}>
            <Logo size={200} draw={draw} glow={0.6 + 0.4 * Math.sin(frame / 12)} />
          </div>
          <div style={{display: 'flex', width: 740 * slide, overflow: 'hidden', paddingBottom: 30, marginBottom: -30, position: 'relative'}}>
            {word.split('').map((ch, i) => {
              const p = interpolate(frame - 48 - i * 3, [0, 20], [0, 1], {...clamp, easing: ease});
              return (
                <span
                  key={i}
                  style={{
                    fontFamily: font,
                    fontSize: 190,
                    fontWeight: 800,
                    letterSpacing: '-0.05em',
                    color: c.text,
                    display: 'inline-block',
                    opacity: p,
                    transform: `translateX(${(1 - p) * 60}px)`,
                    filter: `blur(${(1 - p) * 12}px)`,
                  }}
                >
                  {ch}
                </span>
              );
            })}
            {/* Light sweep over the wordmark. */}
            <div
              style={{
                position: 'absolute',
                inset: 0,
                background: `linear-gradient(100deg, transparent ${sweep - 12}%, rgba(255,255,255,0.55) ${sweep}%, transparent ${sweep + 12}%)`,
                mixBlendMode: 'overlay',
              }}
            />
          </div>
        </div>
        <Tagline />
        <Chips />
      </AbsoluteFill>
    </AbsoluteFill>
  );
};

const Tagline: React.FC = () => {
  const frame = useCurrentFrame();
  const p = interpolate(frame, [78, 105], [0, 1], {...clamp, easing: ease});
  return (
    <div style={{fontFamily: font, fontSize: 52, fontWeight: 500, color: '#c9c8c3', letterSpacing: '-0.02em', opacity: p, transform: `translateY(${(1 - p) * 24}px)`}}>
      A lightweight AI gateway for <span style={{color: c.text, fontWeight: 700}}>coding agents</span>.
    </div>
  );
};

const Chips: React.FC = () => {
  const frame = useCurrentFrame();
  const items = ['One Go binary', 'Anthropic + OpenAI APIs', 'Open source'];
  return (
    <div style={{display: 'flex', gap: 18, marginTop: 6}}>
      {items.map((t, i) => {
        const p = interpolate(frame - 100 - i * 6, [0, 18], [0, 1], {...clamp, easing: ease});
        return (
          <div
            key={t}
            style={{
              fontFamily: monoFont,
              fontSize: 26,
              color: c.blueLight,
              padding: '10px 22px',
              borderRadius: 999,
              border: `1px solid ${c.blue}66`,
              background: `${c.blue}18`,
              opacity: p,
              transform: `translateY(${(1 - p) * 20}px) scale(${0.9 + 0.1 * p})`,
            }}
          >
            {t}
          </div>
        );
      })}
    </div>
  );
};
