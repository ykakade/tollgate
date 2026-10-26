import React from 'react';
import {AbsoluteFill, interpolate, interpolateColors, random, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, shake, Words} from '../components/ui';
import {c, font, monoFont} from '../theme';

// "Providers fail. Agents overspend. Put a gate in front."
export const Hook: React.FC = () => {
  const frame = useCurrentFrame();
  const s = shake(frame, 22, 16, 10);
  const dim = interpolate(frame, [118, 140], [1, 0.28], {...clamp, easing: ease});
  const dollars = interpolate(frame, [70, 150], [0, 1], clamp);
  const spend = (Math.pow(dollars, 2.2) * 4870 + dollars * 17).toLocaleString('en-US', {minimumFractionDigits: 2, maximumFractionDigits: 2});

  return (
    <AbsoluteFill>
      <Background glow={interpolateColors(frame, [40, 100], [c.critical, c.blue])} intensity={0.8} />
      <AbsoluteFill style={{justifyContent: 'center', paddingLeft: 230, transform: `translate(${s.x}px, ${s.y}px)`}}>
        <div style={{display: 'flex', flexDirection: 'column', gap: 26}}>
          <Row opacity={dim}>
            <Words text="Providers fail." size={124} weight={800} delay={2} />
            <Chip delay={18} color={c.critical}>
              <Glitch text="529 Overloaded" frame={frame} start={20} />
            </Chip>
          </Row>
          <Row opacity={dim}>
            <Words text="Agents overspend." size={124} weight={800} delay={52} />
            <Chip delay={68} color={c.warning}>
              <span style={{fontVariantNumeric: 'tabular-nums'}}>${spend}</span>
              <span style={{fontSize: 30}}>↑</span>
            </Chip>
          </Row>
          <Words
            text="Put a gate in front."
            size={124}
            weight={800}
            delay={124}
            stagger={5}
            highlight={{gate: `linear-gradient(90deg, ${c.blueLight}, ${c.blue})`}}
          />
        </div>
      </AbsoluteFill>
      {/* Red flash on the 529 hit. */}
      <AbsoluteFill style={{background: c.critical, opacity: interpolate(frame, [20, 22, 34], [0, 0.18, 0], clamp), mixBlendMode: 'screen'}} />
    </AbsoluteFill>
  );
};

const Row: React.FC<{children: React.ReactNode; opacity: number}> = ({children, opacity}) => (
  <div style={{display: 'flex', alignItems: 'center', gap: 40, opacity, fontFamily: font}}>{children}</div>
);

const Chip: React.FC<{delay: number; color: string; children: React.ReactNode}> = ({delay, color, children}) => {
  const frame = useCurrentFrame();
  const p = interpolate(frame - delay, [0, 16], [0, 1], {...clamp, easing: ease});
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '14px 28px',
        borderRadius: 18,
        fontFamily: monoFont,
        fontSize: 44,
        fontWeight: 700,
        color,
        background: `${color}1f`,
        border: `2px solid ${color}88`,
        boxShadow: `0 0 50px ${color}44`,
        opacity: p,
        transform: `scale(${0.6 + 0.4 * p}) rotate(${(1 - p) * -6}deg)`,
        marginTop: -6,
      }}
    >
      {children}
    </div>
  );
};

// RGB split that jitters for a short time after `start`.
const Glitch: React.FC<{text: string; frame: number; start: number}> = ({text, frame, start}) => {
  const k = interpolate(frame - start, [0, 26], [1, 0], clamp);
  const jx = (random(`g${frame}`) - 0.5) * 18 * k;
  const jy = (random(`h${frame}`) - 0.5) * 6 * k;
  return (
    <span style={{position: 'relative', display: 'inline-block', whiteSpace: 'nowrap'}}>
      <span style={{position: 'absolute', left: jx, top: jy, color: '#00e5ff', opacity: 0.7 * k, mixBlendMode: 'screen'}}>{text}</span>
      <span style={{position: 'absolute', left: -jx, top: -jy, color: '#ff2a6d', opacity: 0.7 * k, mixBlendMode: 'screen'}}>{text}</span>
      <span style={{position: 'relative'}}>{text}</span>
    </span>
  );
};
