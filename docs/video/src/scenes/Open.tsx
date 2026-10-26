import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, Logo, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// The first seconds: the mark, the name, what it is, and three facts.
export const Open: React.FC = () => {
  const frame = useCurrentFrame();
  const pop = useSpring(0, {damping: 13, stiffness: 160});
  const ring = interpolate(frame, [4, 60], [0, 1], clamp);
  const sweep = interpolate(frame, [40, 90], [-30, 130], clamp);
  const tag = interpolate(frame, [34, 60], [0, 1], {...clamp, easing: ease});
  const word = 'Tollgate';

  return (
    <AbsoluteFill>
      <Background intensity={1.25} />
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center'}}>
        <div style={{width: 320, height: 320, borderRadius: '50%', border: `3px solid ${c.blueLight}`, opacity: (1 - ring) * 0.55, transform: `translateY(-150px) scale(${0.4 + ring * 3.6})`}} />
      </AbsoluteFill>
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center', flexDirection: 'column'}}>
        <div style={{display: 'flex', alignItems: 'center', gap: 46}}>
          <div style={{transform: `scale(${0.3 + pop * 0.7}) rotate(${(1 - pop) * -16}deg)`, opacity: Math.min(1, pop * 3)}}>
            <Logo size={176} glow={0.9} />
          </div>
          <div style={{display: 'flex', position: 'relative', paddingBottom: 24, marginBottom: -24}}>
            {word.split('').map((ch, i) => {
              const p = interpolate(frame - 8 - i * 2.5, [0, 20], [0, 1], {...clamp, easing: ease});
              return (
                <span
                  key={i}
                  style={{
                    fontFamily: font,
                    fontSize: 184,
                    fontWeight: 800,
                    letterSpacing: '-0.05em',
                    color: c.text,
                    display: 'inline-block',
                    opacity: p,
                    transform: `translateX(${(1 - p) * 50}px)`,
                    filter: `blur(${(1 - p) * 10}px)`,
                  }}
                >
                  {ch}
                </span>
              );
            })}
            <div style={{position: 'absolute', inset: 0, background: `linear-gradient(100deg, transparent ${sweep - 12}%, rgba(255,255,255,0.5) ${sweep}%, transparent ${sweep + 12}%)`, mixBlendMode: 'overlay'}} />
          </div>
        </div>
        <div style={{fontFamily: font, fontSize: 60, fontWeight: 600, color: '#d6d5d0', letterSpacing: '-0.025em', marginTop: 18, opacity: tag, transform: `translateY(${(1 - tag) * 24}px)`}}>
          The AI gateway for <span style={{color: c.text, fontWeight: 800}}>coding agents</span>.
        </div>
        <div style={{display: 'flex', gap: 22, marginTop: 58}}>
          {(
            [
              ['89 µs', 'demo overhead'],
              ['Failover', 'before the first byte'],
              ['Hard budgets', 'per agent session'],
            ] as const
          ).map(([a, b], i) => {
            const p = interpolate(frame - 62 - i * 8, [0, 20], [0, 1], {...clamp, easing: ease});
            return (
              <div
                key={a}
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: 14,
                  padding: '18px 30px',
                  borderRadius: 18,
                  border: `1px solid ${c.blue}77`,
                  background: `${c.blue}1c`,
                  opacity: p,
                  transform: `translateY(${(1 - p) * 26}px) scale(${0.92 + 0.08 * p})`,
                }}
              >
                <span style={{fontFamily: font, fontWeight: 800, fontSize: 38, color: c.text, letterSpacing: '-0.02em'}}>{a}</span>
                <span style={{fontFamily: monoFont, fontSize: 29, color: c.blueLight}}>{b}</span>
              </div>
            );
          })}
        </div>
      </AbsoluteFill>
    </AbsoluteFill>
  );
};
