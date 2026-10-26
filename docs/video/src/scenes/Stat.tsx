import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// "89 µs" from overhead_us in the log of the failover scene.
export const Stat: React.FC = () => {
  const frame = useCurrentFrame();
  const n = Math.round(interpolate(frame, [6, 50], [0, 89], {...clamp, easing: ease}));
  const pop = useSpring(4, {damping: 14});
  const sub = interpolate(frame, [44, 70], [0, 1], {...clamp, easing: ease});
  const sub2 = interpolate(frame, [70, 96], [0, 1], {...clamp, easing: ease});

  return (
    <AbsoluteFill>
      <Background intensity={1.3} />
      {/* Rings that pulse out from the number. */}
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center'}}>
        {[0, 1, 2].map((i) => {
          const t = ((frame + i * 30) % 90) / 90;
          return (
            <div
              key={i}
              style={{position: 'absolute', width: 520, height: 520, borderRadius: '50%', border: `2px solid ${c.blue}`, opacity: (1 - t) * 0.35, transform: `translateY(-60px) scale(${0.6 + t * 1.8})`}}
            />
          );
        })}
      </AbsoluteFill>
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center', flexDirection: 'column'}}>
        <div style={{display: 'flex', alignItems: 'baseline', transform: `scale(${0.7 + 0.3 * pop})`, opacity: pop}}>
          <span
            style={{
              fontFamily: font,
              fontWeight: 800,
              fontSize: 400,
              letterSpacing: '-0.06em',
              lineHeight: 1,
              fontVariantNumeric: 'tabular-nums',
              backgroundImage: `linear-gradient(180deg, #ffffff 20%, ${c.blueLight} 120%)`,
              WebkitBackgroundClip: 'text',
              backgroundClip: 'text',
              color: 'transparent',
              paddingRight: 20,
            }}
          >
            {n}
          </span>
          <span style={{fontFamily: font, fontWeight: 700, fontSize: 170, color: c.blueLight, letterSpacing: '-0.04em'}}>µs</span>
        </div>
        <div style={{fontFamily: font, fontSize: 54, fontWeight: 600, color: c.text, letterSpacing: '-0.02em', opacity: sub, transform: `translateY(${(1 - sub) * 20}px)`}}>
          of proxy overhead. Two attempts. One answer.
        </div>
        <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted, marginTop: 26, opacity: sub2, transform: `translateY(${(1 - sub2) * 16}px)`}}>
          Streams pass through byte for byte.
        </div>
      </AbsoluteFill>
    </AbsoluteFill>
  );
};
