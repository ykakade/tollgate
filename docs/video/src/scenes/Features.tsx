import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, Words} from '../components/ui';
import {c, font} from '../theme';

// Icon paths are from Lucide (ISC license).
const icons: Record<string, React.ReactNode> = {
  gauge: (
    <>
      <path d="m12 14 4-4" />
      <path d="M3.34 19a10 10 0 1 1 17.32 0" />
    </>
  ),
  zap: <path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z" />,
  retry: (
    <>
      <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
      <path d="M21 3v5h-5" />
      <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
      <path d="M8 16H3v5" />
    </>
  ),
  code: (
    <>
      <path d="m16 18 6-6-6-6" />
      <path d="m8 6-6 6 6 6" />
    </>
  ),
  cloud: <path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" />,
  activity: <path d="M22 12h-4l-3 9L9 3l-3 9H2" />,
  shield: <path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z" />,
  power: (
    <>
      <path d="M12 2v10" />
      <path d="M18.4 6.6a9 9 0 1 1-12.77.04" />
    </>
  ),
};

// Each item is from the "What Tollgate does" list in the README.
const items: [keyof typeof icons, string, string][] = [
  ['gauge', 'Rate limits', 'Requests and tokens per minute, shared with Redis'],
  ['zap', 'Circuit breakers', 'For each provider, model and region'],
  ['retry', 'Retries', 'Ordered targets and a retry budget'],
  ['code', 'Native APIs', '/v1/messages and /v1/chat/completions'],
  ['cloud', 'Cloud and local', 'Claude on Vertex AI, or local Ollama'],
  ['activity', 'Observability', 'Prometheus metrics, OpenTelemetry traces'],
  ['shield', 'Redaction', 'Opt-in personal data redaction'],
  ['power', 'Graceful drain', 'SIGTERM lets open streams finish'],
];

export const Features: React.FC = () => {
  const frame = useCurrentFrame();
  return (
    <AbsoluteFill>
      <Background />
      <AbsoluteFill style={{alignItems: 'center', paddingTop: 110}}>
        <Words text="And the rest of what a gateway needs." size={68} weight={700} delay={2} stagger={3} />
        <div style={{display: 'grid', gridTemplateColumns: 'repeat(4, 390px)', gap: 26, marginTop: 80}}>
          {items.map(([icon, title, sub], i) => {
            const d = 14 + (i % 4) * 5 + Math.floor(i / 4) * 12;
            const p = interpolate(frame - d, [0, 24], [0, 1], {...clamp, easing: ease});
            const sweep = interpolate(frame - d - 16, [0, 30], [-40, 140], clamp);
            return (
              <div
                key={title}
                style={{
                  position: 'relative',
                  height: 262,
                  padding: '34px 32px',
                  borderRadius: 24,
                  background: 'linear-gradient(180deg, rgba(36,36,34,0.9), rgba(20,20,19,0.9))',
                  border: `1px solid ${c.border}`,
                  boxShadow: '0 30px 60px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.06)',
                  overflow: 'hidden',
                  opacity: p,
                  transform: `translateY(${(1 - p) * 60}px) scale(${0.92 + 0.08 * p})`,
                }}
              >
                <div style={{position: 'absolute', inset: 0, background: `linear-gradient(110deg, transparent ${sweep - 20}%, rgba(109,167,236,0.16) ${sweep}%, transparent ${sweep + 20}%)`}} />
                <div style={{width: 72, height: 72, borderRadius: 18, background: `${c.blue}22`, border: `1px solid ${c.blue}55`, display: 'flex', alignItems: 'center', justifyContent: 'center'}}>
                  <svg width={38} height={38} viewBox="0 0 24 24" fill="none" stroke={c.blueLight} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                    {icons[icon]}
                  </svg>
                </div>
                <div style={{fontFamily: font, fontSize: 38, fontWeight: 700, color: c.text, marginTop: 30, letterSpacing: '-0.02em'}}>{title}</div>
                <div style={{fontFamily: font, fontSize: 25, color: c.muted, marginTop: 10, lineHeight: 1.35}}>{sub}</div>
              </div>
            );
          })}
        </div>
      </AbsoluteFill>
    </AbsoluteFill>
  );
};
