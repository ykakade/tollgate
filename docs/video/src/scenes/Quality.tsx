import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, clamp, ease, SceneTitle} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Facts about the repository. Count them again before a new render:
//   tests:    grep -rhoE '^func Test' --include=*_test.go . | wc -l
//   coverage: go test -coverprofile=c.out ./... && go tool cover -func=c.out | tail -1
//   fuzz:     grep -rhoE '^func Fuzz' --include=*_test.go . | wc -l
//   ADRs:     ls docs/adr/0*.md | wc -l
const STATS: {n: number; fmt: (v: number) => string; label: string; sub: string}[] = [
  {n: 250, fmt: (v) => `${Math.round(v)}`, label: 'Go tests', sub: 'race detector on in CI'},
  {n: 90.8, fmt: (v) => `${v.toFixed(1)}%`, label: 'Statement coverage', sub: 'go test -cover ./...'},
  {n: 33, fmt: (v) => `${Math.round(v)}`, label: 'Decision records', sub: 'one ADR per design choice'},
  {n: 4, fmt: (v) => `${Math.round(v)}`, label: 'Fuzz targets', sub: 'SSE, preflight, PII, config'},
  {n: 2, fmt: (v) => `${Math.round(v)}`, label: 'Official SDKs', sub: 'Anthropic + OpenAI tests'},
  {n: 3, fmt: (v) => `${Math.round(v)}`, label: 'Copies, one limit', sub: 'cluster tests on real Redis'},
];

export const Quality: React.FC = () => {
  const frame = useCurrentFrame();
  const foot = interpolate(frame, [150, 175], [0, 1], {...clamp, easing: ease});
  return (
    <AbsoluteFill>
      <Background />
      <SceneTitle n="09" label="Engineering" title="Tested like infrastructure." highlight={{infrastructure: c.blueLight}} />
      <div style={{position: 'absolute', left: 120, top: 300, width: 1680, display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 28}}>
        {STATS.map((s, i) => {
          const d = 14 + i * 7;
          const p = interpolate(frame - d, [0, 22], [0, 1], {...clamp, easing: ease});
          const v = interpolate(frame - d, [0, 50], [0, s.n], {...clamp, easing: ease});
          return (
            <div
              key={s.label}
              style={{
                height: 280,
                boxSizing: 'border-box',
                padding: '34px 40px',
                borderRadius: 24,
                background: 'linear-gradient(180deg, rgba(36,36,34,0.92), rgba(20,20,19,0.92))',
                border: `1px solid ${c.border}`,
                boxShadow: '0 30px 60px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.06)',
                opacity: p,
                transform: `translateY(${(1 - p) * 40}px)`,
              }}
            >
              <div
                style={{
                  fontFamily: font,
                  fontSize: 104,
                  fontWeight: 800,
                  letterSpacing: '-0.05em',
                  lineHeight: 1,
                  fontVariantNumeric: 'tabular-nums',
                  backgroundImage: `linear-gradient(180deg, #ffffff 30%, ${c.blueLight} 130%)`,
                  WebkitBackgroundClip: 'text',
                  backgroundClip: 'text',
                  color: 'transparent',
                }}
              >
                {s.fmt(v)}
              </div>
              <div style={{fontFamily: font, fontSize: 38, fontWeight: 700, color: c.text, marginTop: 20, letterSpacing: '-0.02em'}}>{s.label}</div>
              <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted, marginTop: 8}}>{s.sub}</div>
            </div>
          );
        })}
      </div>
      <div style={{position: 'absolute', left: 0, right: 0, top: 940, textAlign: 'center', fontFamily: monoFont, fontSize: 29, color: c.muted, opacity: foot, transform: `translateY(${(1 - foot) * 16}px)`}}>
        Signed releases (cosign)<span style={{color: c.blueLight}}> · </span>SLSA build provenance<span style={{color: c.blueLight}}> · </span>govulncheck in CI
      </div>
    </AbsoluteFill>
  );
};
