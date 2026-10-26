import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Background, Card, clamp, ease, SceneTitle, typed, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Three agent features. The numbers are from docs/agent-features.md (the
// sticky routing replay) and from the dashboard demo.
export const Agents: React.FC = () => (
  <AbsoluteFill>
    <Background />
    <SceneTitle n="06" label="Agent sessions" title="Built for long-running coding agents." highlight={{agents: c.blueLight}} />
    <Panel x={120} delay={10} title="Sticky routing" text="After a fallback, the session stays on the target with the warm prompt cache.">
      <Sticky />
    </Panel>
    <Panel x={700} delay={22} title="PII redaction" text="Personal data is masked before the provider and restored in the response.">
      <Redact />
    </Panel>
    <Panel x={1280} delay={34} title="Session budgets" text="Each agent task gets its own budget, from a header or derived from the prompt.">
      <Session />
    </Panel>
  </AbsoluteFill>
);

const Panel: React.FC<{x: number; delay: number; title: string; text: string; children: React.ReactNode}> = ({x, delay, title, text, children}) => {
  const p = useSpring(delay);
  return (
    <div style={{position: 'absolute', left: x, top: 300, width: 540, opacity: p, transform: `translateY(${(1 - p) * 50}px)`}}>
      <Card style={{padding: '34px 36px', height: 640, boxSizing: 'border-box', display: 'flex', flexDirection: 'column'}}>
        <div style={{fontFamily: font, fontSize: 40, fontWeight: 800, color: c.text, letterSpacing: '-0.02em'}}>{title}</div>
        <div style={{fontFamily: font, fontSize: 29, lineHeight: 1.38, color: '#bdbcb7', marginTop: 12, minHeight: 120}}>{text}</div>
        <div style={{flex: 1, marginTop: 18}}>{children}</div>
      </Card>
    </div>
  );
};

const Sticky: React.FC = () => {
  const frame = useCurrentFrame();
  const t = interpolate(frame, [70, 130], [0, 1], {...clamp, easing: ease});
  const off = 0.39032;
  const on = 0.294224;
  const max = 0.42;
  const save = interpolate(frame, [130, 150], [0, 1], {...clamp, easing: ease});
  const Bar: React.FC<{label: string; v: number; col: string}> = ({label, v, col}) => (
    <div style={{marginTop: 18}}>
      <div style={{display: 'flex', justifyContent: 'space-between', fontFamily: monoFont, fontSize: 27, color: c.muted}}>
        <span>{label}</span>
        <span style={{color: c.text}}>${(v * t).toFixed(3)}</span>
      </div>
      <div style={{height: 26, borderRadius: 8, background: 'rgba(255,255,255,0.06)', marginTop: 10}}>
        <div style={{width: `${(v / max) * t * 100}%`, height: '100%', borderRadius: 8, background: col}} />
      </div>
    </div>
  );
  return (
    <div>
      <div style={{fontFamily: monoFont, fontSize: 25, color: c.muted}}>replay · 6 min outage</div>
      <Bar label="sticky off" v={off} col="#5f5e5a" />
      <Bar label="sticky on" v={on} col={c.blue} />
      <div style={{marginTop: 30, display: 'flex', alignItems: 'baseline', gap: 14, opacity: save, transform: `translateY(${(1 - save) * 14}px)`}}>
        <span style={{fontFamily: font, fontSize: 64, fontWeight: 800, color: c.good, letterSpacing: '-0.03em'}}>−25%</span>
        <span style={{fontFamily: font, fontSize: 30, color: c.text}}>cost</span>
      </div>
    </div>
  );
};

const Redact: React.FC = () => {
  const frame = useCurrentFrame();
  const swap = frame >= 110;
  const back = interpolate(frame, [170, 190], [0, 1], {...clamp, easing: ease});
  const Line: React.FC<{label: string; value: React.ReactNode; col?: string; o?: number}> = ({label, value, col = c.muted, o = 1}) => (
    <div style={{marginTop: 18, opacity: o}}>
      <div style={{fontFamily: monoFont, fontSize: 24, color: col, letterSpacing: '0.06em', textTransform: 'uppercase'}}>{label}</div>
      <div style={{marginTop: 8, padding: '14px 18px', borderRadius: 12, background: '#0f0f0e', border: `1px solid ${c.border}`, fontFamily: monoFont, fontSize: 28, color: c.text, whiteSpace: 'nowrap', overflow: 'hidden'}}>{value}</div>
    </div>
  );
  const flash = interpolate(frame, [110, 114, 130], [0, 1, 0], clamp);
  return (
    <div>
      <Line label="from the agent" value={<>Email <span style={{color: '#a5d6a7'}}>{typed('ana@corp.com', frame, 60, 30)}</span></>} />
      <Line
        label="to the provider"
        col={c.blueLight}
        o={interpolate(frame, [96, 110], [0, 1], clamp)}
        value={
          <>
            Email{' '}
            <span style={{color: '#f6c177', background: `rgba(250,178,25,${0.25 * flash})`, borderRadius: 6}}>{swap ? '[EMAIL_1]' : 'ana@corp.com'}</span>
          </>
        }
      />
      <Line
        label="back to the agent"
        col={c.good}
        o={back}
        value={
          <>
            Sent to <span style={{color: '#a5d6a7'}}>ana@corp.com</span>
          </>
        }
      />
    </div>
  );
};

const Session: React.FC = () => {
  const frame = useCurrentFrame();
  const fill = interpolate(frame, [80, 200], [0, 0.62], {...clamp, easing: ease});
  const left = 5 - fill * 5;
  return (
    <div>
      <div style={{padding: '14px 18px', borderRadius: 12, background: '#0f0f0e', border: `1px solid ${c.border}`, fontFamily: monoFont, fontSize: 25, whiteSpace: 'nowrap'}}>
        <span style={{color: '#8fb8f0'}}>x-tollgate-session:</span> <span style={{color: '#a5d6a7'}}>task-42</span>
      </div>
      <div style={{display: 'flex', alignItems: 'baseline', gap: 12, marginTop: 30}}>
        <span style={{fontFamily: font, fontSize: 64, fontWeight: 800, color: c.text, letterSpacing: '-0.03em', fontVariantNumeric: 'tabular-nums'}}>${left.toFixed(2)}</span>
        <span style={{fontFamily: font, fontSize: 30, color: c.muted}}>left of $5.00</span>
      </div>
      <div style={{height: 18, borderRadius: 9, background: 'rgba(255,255,255,0.07)', marginTop: 12}}>
        <div style={{width: `${fill * 100}%`, height: '100%', borderRadius: 9, background: c.blue}} />
      </div>
      <div style={{fontFamily: font, fontSize: 29, color: '#bdbcb7', marginTop: 26, lineHeight: 1.4}}>
        Warning at 80%. At the hard limit, the stream stops with a clean error event.
      </div>
    </div>
  );
};
