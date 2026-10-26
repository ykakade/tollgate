import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, Cursor, ease, Logo, typed, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

const INSTALL = 'go install github.com/ykakade/tollgate/cmd/tollgate@latest';

export const End: React.FC = () => {
  const frame = useCurrentFrame();
  const pop = useSpring(0, {damping: 13});
  const title = interpolate(frame, [6, 34], [0, 1], {...clamp, easing: ease});
  const tag = interpolate(frame, [24, 50], [0, 1], {...clamp, easing: ease});
  const box = interpolate(frame, [44, 70], [0, 1], {...clamp, easing: ease});
  const repo = interpolate(frame, [130, 156], [0, 1], {...clamp, easing: ease});
  const line = interpolate(frame, [150, 190], [0, 1], {...clamp, easing: ease});

  return (
    <AbsoluteFill>
      <Background intensity={1.4} />
      <AbsoluteFill style={{justifyContent: 'center', alignItems: 'center', flexDirection: 'column'}}>
        <div style={{display: 'flex', alignItems: 'center', gap: 40}}>
          <div style={{transform: `scale(${pop})`}}>
            <Logo size={150} glow={1} />
          </div>
          <div style={{fontFamily: font, fontWeight: 800, fontSize: 150, letterSpacing: '-0.05em', color: c.text, opacity: title, transform: `translateX(${(1 - title) * 40}px)`, filter: `blur(${(1 - title) * 10}px)`}}>
            Tollgate
          </div>
        </div>
        <div style={{fontFamily: font, fontSize: 46, fontWeight: 500, color: '#c9c8c3', marginTop: 18, opacity: tag, transform: `translateY(${(1 - tag) * 20}px)`}}>
          Cost, rate limits and failover for coding agents.
        </div>
        <div
          style={{
            marginTop: 64,
            padding: '24px 36px',
            borderRadius: 18,
            background: 'rgba(16,16,15,0.9)',
            border: `1px solid ${c.border}`,
            boxShadow: `0 30px 80px rgba(0,0,0,0.6), 0 0 60px ${c.blue}22`,
            fontFamily: monoFont,
            fontSize: 32,
            color: c.text,
            minWidth: 1140,
            opacity: box,
            transform: `translateY(${(1 - box) * 30}px)`,
          }}
        >
          <span style={{color: c.blueLight}}>$ </span>
          {typed(INSTALL, frame, 60, 70)}
          <Cursor />
        </div>
        <div style={{marginTop: 54, display: 'flex', flexDirection: 'column', alignItems: 'center', opacity: repo, transform: `translateY(${(1 - repo) * 20}px)`}}>
          <div style={{fontFamily: monoFont, fontSize: 40, fontWeight: 700, color: c.text}}>github.com/ykakade/tollgate</div>
          <div style={{height: 3, width: 640 * line, marginTop: 10, background: `linear-gradient(90deg, transparent, ${c.blueLight}, transparent)`}} />
          <div style={{fontFamily: font, fontSize: 30, color: c.muted, marginTop: 18}}>Open source · Apache-2.0</div>
        </div>
      </AbsoluteFill>
    </AbsoluteFill>
  );
};
