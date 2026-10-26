import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {getLength, getPointAtLength} from '@remotion/paths';
import {Background, Card, clamp, Cursor, ease, Logo, Pill, SceneTitle, shake, Terminal, typed, useSpring} from '../components/ui';
import {c, font, monoFont} from '../theme';

// Positions of the diagram nodes.
const AGENT = {x: 380, y: 480};
const GATE = {x: 960, y: 480};
const PRIMARY = {x: 1570, y: 365};
const BACKUP = {x: 1570, y: 615};

const pAgentGate = `M ${AGENT.x + 260} ${AGENT.y} L ${GATE.x - 100} ${GATE.y}`;
const pGatePrimary = `M ${GATE.x + 100} ${GATE.y} C ${GATE.x + 300} ${GATE.y}, ${PRIMARY.x - 420} ${PRIMARY.y}, ${PRIMARY.x - 220} ${PRIMARY.y}`;
const pGateBackup = `M ${GATE.x + 100} ${GATE.y} C ${GATE.x + 300} ${GATE.y}, ${BACKUP.x - 420} ${BACKUP.y}, ${BACKUP.x - 220} ${BACKUP.y}`;

// Timeline, in frames.
const T = {
  toGate: [40, 70],
  toPrimary: [72, 100],
  fail: 100,
  log1: 112,
  toBackup: [134, 162],
  ok: 162,
  streamStart: 170,
  reply: 186,
  log2: 250,
  focus: 300,
};

const CMD = `tail -f tollgate.log | jq -c '{msg, provider, reason, attempts, overhead_us} | del(..|nulls)'`;

export const Failover: React.FC = () => {
  const frame = useCurrentFrame();
  const s = shake(frame, T.fail, 14, 8);
  const failed = frame >= T.fail;
  const ok = frame >= T.ok;

  return (
    <AbsoluteFill>
      <Background />
      <SceneTitle n="03" label="Failover" title="Primary down? Tollgate fails over." highlight={{fails: c.blueLight, over: c.blueLight}} />

      <AbsoluteFill style={{transform: `translate(${s.x}px, ${s.y}px)`}}>
        <svg width={1920} height={1080} style={{position: 'absolute', inset: 0}}>
          <Edge d={pAgentGate} start={14} color={c.blueLight} />
          <Edge d={pGatePrimary} start={20} color={failed ? c.critical : c.blueLight} dashed={failed} />
          <Edge d={pGateBackup} start={26} color={ok ? c.good : 'rgba(255,255,255,0.25)'} />
          <Packet d={pAgentGate} range={T.toGate} color={c.blueLight} />
          <Packet d={pGatePrimary} range={T.toPrimary} color={c.blueLight} />
          <Packet d={pGateBackup} range={T.toBackup} color={c.good} />
          <Stream />
        </svg>

        <Node x={AGENT.x} y={AGENT.y} w={520} delay={4}>
          <Label>Claude Code</Label>
          <div style={{fontFamily: monoFont, fontSize: 26, color: c.muted, marginTop: 12, minHeight: 120, lineHeight: 1.45}}>
            <span style={{color: c.blueLight}}>$</span> {typed('claude -p "Say hello"', frame, 14, 40)}
            <br />
            <span style={{color: c.text}}>{typed('Hello from the Tollgate fake provider.', frame, T.reply, 34)}</span>
          </div>
        </Node>

        <GateNode />

        <Node x={PRIMARY.x} y={PRIMARY.y} w={440} delay={10} tint={failed ? c.critical : undefined} jolt={T.fail}>
          <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'center'}}>
            <Label>primary</Label>
            <Status at={T.fail} color={c.critical} text="529" />
          </div>
          <Sub>claude-sonnet-5-5</Sub>
        </Node>
        <Node x={BACKUP.x} y={BACKUP.y} w={440} delay={14} tint={ok ? c.good : undefined}>
          <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'center'}}>
            <Label>backup</Label>
            <Status at={T.ok} color={c.good} text="200" />
          </div>
          <Sub>claude-sonnet-5-5</Sub>
        </Node>
      </AbsoluteFill>

      <LogPanel />
    </AbsoluteFill>
  );
};

const Edge: React.FC<{d: string; start: number; color: string; dashed?: boolean}> = ({d, start, color, dashed}) => {
  const frame = useCurrentFrame();
  const len = getLength(d);
  const p = interpolate(frame - start, [0, 26], [0, 1], {...clamp, easing: ease});
  return (
    <g>
      <path d={d} stroke={color} strokeOpacity={0.18} strokeWidth={14} fill="none" strokeLinecap="round" style={{filter: 'blur(6px)'}} strokeDasharray={len} strokeDashoffset={len * (1 - p)} />
      <path
        d={d}
        stroke={color}
        strokeWidth={3}
        fill="none"
        strokeLinecap="round"
        strokeDasharray={dashed ? '10 12' : len}
        strokeDashoffset={dashed ? -frame * 0.6 : len * (1 - p)}
        opacity={0.9}
      />
    </g>
  );
};

// A glowing dot that moves along a path during `range`.
const Packet: React.FC<{d: string; range: number[]; color: string}> = ({d, range, color}) => {
  const frame = useCurrentFrame();
  if (frame < range[0] || frame > range[1] + 4) return null;
  const len = getLength(d);
  const t = interpolate(frame, range, [0, 1], {...clamp, easing: ease});
  const pt = getPointAtLength(d, t * len)!;
  const tail = getPointAtLength(d, Math.max(0, t - 0.08) * len)!;
  return (
    <g>
      <line x1={tail.x} y1={tail.y} x2={pt.x} y2={pt.y} stroke={color} strokeWidth={6} strokeLinecap="round" opacity={0.5} />
      <circle cx={pt.x} cy={pt.y} r={18} fill={color} opacity={0.25} />
      <circle cx={pt.x} cy={pt.y} r={8} fill="#fff" />
    </g>
  );
};

// Response tokens that flow back from the backup to the agent.
const Stream: React.FC = () => {
  const frame = useCurrentFrame();
  if (frame < T.streamStart) return null;
  const back = `M ${BACKUP.x - 220} ${BACKUP.y} C ${BACKUP.x - 420} ${BACKUP.y}, ${GATE.x + 300} ${GATE.y}, ${GATE.x + 100} ${GATE.y} L ${GATE.x - 100} ${GATE.y} L ${AGENT.x + 260} ${AGENT.y}`;
  const len = getLength(back);
  const dots = [];
  for (let i = 0; i < 14; i++) {
    const born = T.streamStart + i * 6;
    const age = frame - born;
    if (age < 0 || age > 50) continue;
    const t = interpolate(age, [0, 50], [0, 1], clamp);
    const pt = getPointAtLength(back, t * len)!;
    dots.push(<circle key={i} cx={pt.x} cy={pt.y} r={6} fill={c.good} opacity={0.9 - t * 0.3} style={{filter: `drop-shadow(0 0 8px ${c.good})`}} />);
  }
  return <g>{dots}</g>;
};

const Node: React.FC<{x: number; y: number; w: number; delay: number; tint?: string; jolt?: number; children: React.ReactNode}> = ({x, y, w, delay, tint, jolt, children}) => {
  const frame = useCurrentFrame();
  const p = useSpring(delay);
  const j = jolt !== undefined ? shake(frame, jolt, 18, 10) : {x: 0, y: 0};
  return (
    <div style={{position: 'absolute', left: x - w / 2 + j.x, top: y - 70 + j.y, width: w, opacity: p, transform: `translateY(${(1 - p) * 40}px) scale(${0.9 + 0.1 * p})`}}>
      <Card
        style={{padding: '24px 28px', borderColor: tint ? `${tint}aa` : c.border, transition: 'none'}}
        glow={tint ? `${tint}40` : undefined}
      >
        {children}
      </Card>
    </div>
  );
};

const GateNode: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(0, {damping: 13});
  const pulse = frame >= T.fail && frame < T.toBackup[0] ? Math.sin((frame - T.fail) / 3) * 0.5 + 0.5 : 0;
  const spin = interpolate(frame, [T.fail + 6, T.toBackup[0]], [0, 360], {...clamp, easing: ease});
  const reroute = frame >= T.fail + 4 && frame < T.ok + 30;
  return (
    <div style={{position: 'absolute', left: GATE.x - 100, top: GATE.y - 100, width: 200, height: 200, transform: `scale(${p})`}}>
      <div style={{position: 'absolute', inset: -26, borderRadius: 60, border: `2px solid ${c.blueLight}`, opacity: 0.25 + pulse * 0.5, transform: `rotate(${spin}deg)`, borderStyle: 'dashed'}} />
      <Logo size={200} glow={0.8 + pulse * 0.6} />
      <div style={{position: 'absolute', top: 222, width: 400, left: -100, textAlign: 'center', fontFamily: font, fontWeight: 700, fontSize: 36, color: c.text}}>Tollgate</div>
      <div
        style={{
          position: 'absolute',
          top: 272,
          width: 400,
          left: -100,
          textAlign: 'center',
          fontFamily: monoFont,
          fontSize: 28,
          color: c.warning,
          opacity: reroute ? 1 : 0,
        }}
      >
        retrying on backup…
      </div>
    </div>
  );
};

const Status: React.FC<{at: number; color: string; text: string}> = ({at, color, text}) => {
  const p = useSpring(at, {damping: 10, stiffness: 200});
  return (
    <Pill color={color} style={{fontSize: 30, transform: `scale(${p})`, opacity: p > 0.01 ? 1 : 0, fontWeight: 700}}>
      {text}
    </Pill>
  );
};

const Label: React.FC<{children: React.ReactNode}> = ({children}) => (
  <div style={{fontFamily: font, fontSize: 36, fontWeight: 700, color: c.text, letterSpacing: '-0.02em'}}>{children}</div>
);
const Sub: React.FC<{children: React.ReactNode}> = ({children}) => (
  <div style={{fontFamily: monoFont, fontSize: 25, color: c.muted, marginTop: 8}}>{children}</div>
);

// The real log lines, through jq, as in docs/demo/demo.tape.
const LogPanel: React.FC = () => {
  const frame = useCurrentFrame();
  const p = useSpring(24);
  const focus = interpolate(frame, [T.focus, T.focus + 20], [0, 1], {...clamp, easing: ease});
  const cmd = typed(CMD, frame, 30, 140);
  const show1 = frame >= T.log1;
  const show2 = frame >= T.log2;
  const k = (s: string) => <span style={{color: '#8b8a85'}}>"{s}":</span>;
  const str = (s: string, col = '#a5d6a7') => <span style={{color: col}}>"{s}"</span>;
  const num = (n: number, hl = false) => (
    <span
      style={{
        color: '#f6c177',
        background: hl ? `rgba(250,178,25,${0.22 * focus})` : undefined,
        boxShadow: hl ? `0 0 0 ${2 * focus}px rgba(250,178,25,${0.7 * focus})` : undefined,
        borderRadius: 6,
        padding: hl ? '0 4px' : undefined,
      }}
    >
      {n}
    </span>
  );
  const lineIn = (at: number) => {
    const q = interpolate(frame - at, [0, 12], [0, 1], {...clamp, easing: ease});
    return {opacity: q, transform: `translateX(${(1 - q) * -20}px)`, whiteSpace: 'nowrap' as const};
  };
  return (
    <div style={{position: 'absolute', left: 120, right: 120, top: 790, opacity: p, transform: `translateY(${(1 - p) * 60}px)`}}>
      <Terminal title="tollgate.log" style={{}}>
        <div style={{fontSize: 26, whiteSpace: 'nowrap'}}>
          <span style={{color: c.blueLight}}>$</span> {cmd}
          {!show1 && <Cursor />}
        </div>
        <div style={{fontSize: 26, minHeight: 42, ...lineIn(T.log1)}}>
          {show1 && (
            <>
              {'{'}
              {k('msg')}
              {str('upstream attempt failed, trying again', c.warning)},{k('provider')}
              {str('primary')},{k('reason')}
              {str('status 529', '#ff8a8a')}
              {'}'}
            </>
          )}
        </div>
        <div style={{fontSize: 26, minHeight: 42, ...lineIn(T.log2)}}>
          {show2 && (
            <>
              {'{'}
              {k('msg')}
              {str('request done')},{k('provider')}
              {str('backup')},{k('attempts')}
              {num(2, true)},{k('overhead_us')}
              {num(89, true)}
              {'}'}
            </>
          )}
        </div>
      </Terminal>
    </div>
  );
};
