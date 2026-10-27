import React from 'react';
import {AbsoluteFill, Composition, interpolate, Still, useCurrentFrame} from 'remotion';
import {linearTiming, TransitionSeries} from '@remotion/transitions';
import type {TransitionPresentation, TransitionPresentationComponentProps} from '@remotion/transitions';
import {clamp, easeInOut, Logo} from './components/ui';
import {c, font, FPS} from './theme';
import {Open} from './scenes/Open';
import {Architecture} from './scenes/Architecture';
import {Pipeline} from './scenes/Pipeline';
import {Failover} from './scenes/Failover';
import {Resilience} from './scenes/Resilience';
import {Budget} from './scenes/Budget';
import {Agents} from './scenes/Agents';
import {Observability} from './scenes/Observability';
import {Dashboard} from './scenes/Dashboard';
import {Quality} from './scenes/Quality';
import {End} from './scenes/End';
import {Banner, BANNER_H, BANNER_W, Social, SOCIAL_H, SOCIAL_W} from './stills/Banner';
import {Flow, FLOW_H, FLOW_W} from './stills/Flow';

// Rise transition: the old scene moves up and fades, the new one rises in.
// It has no blur, so that text stays sharp in the README WebP.
const Rise: React.FC<TransitionPresentationComponentProps<Record<string, never>>> = ({children, presentationDirection, presentationProgress}) => {
  const p = presentationProgress;
  const entering = presentationDirection === 'entering';
  const y = entering ? (1 - p) * 60 : -p * 60;
  const opacity = entering ? p : 1 - p;
  return <AbsoluteFill style={{transform: `translateY(${y}px)`, opacity}}>{children}</AbsoluteFill>;
};
const rise = (): TransitionPresentation<Record<string, never>> => ({component: Rise, props: {}});

const T = 18;
const scenes: [React.FC, number][] = [
  [Open, 250],
  [Architecture, 380],
  [Pipeline, 980],
  [Failover, 470],
  [Resilience, 450],
  [Budget, 360],
  [Agents, 450],
  [Observability, 400],
  [Dashboard, 660],
  [Quality, 330],
  [End, 330],
];
const starts = scenes.map((_, i) => scenes.slice(0, i).reduce((s, [, d]) => s + d - T, 0));
const total = scenes.reduce((s, [, d]) => s + d, 0) - (scenes.length - 1) * T;

// The mark at the top right and a progress line at the bottom, on all
// scenes except the first and the last.
const Overlay: React.FC = () => {
  const frame = useCurrentFrame();
  const a = starts[1];
  const b = starts[scenes.length - 1] + T;
  const o = interpolate(frame, [a, a + T, b - T, b], [0, 1, 1, 0], clamp);
  if (o <= 0) return null;
  return (
    <AbsoluteFill style={{opacity: o, pointerEvents: 'none'}}>
      <div style={{position: 'absolute', right: 120, top: 40, display: 'flex', alignItems: 'center', gap: 14}}>
        <Logo size={44} />
        <span style={{fontFamily: font, fontSize: 34, fontWeight: 800, letterSpacing: '-0.03em', color: c.text}}>Tollgate</span>
      </div>
      <div style={{position: 'absolute', left: 0, bottom: 0, height: 6, width: `${(frame / total) * 100}%`, background: `linear-gradient(90deg, ${c.blue}, ${c.blueLight})`}} />
    </AbsoluteFill>
  );
};

const Launch: React.FC = () => (
  <AbsoluteFill style={{backgroundColor: c.bg}}>
    <TransitionSeries>
      {scenes.flatMap(([Scene, d], i) => [
        <TransitionSeries.Sequence key={`s${i}`} durationInFrames={d}>
          <Scene />
        </TransitionSeries.Sequence>,
        ...(i < scenes.length - 1 ? [<TransitionSeries.Transition key={`t${i}`} presentation={rise()} timing={linearTiming({durationInFrames: T, easing: easeInOut})} />] : []),
      ])}
    </TransitionSeries>
    <Overlay />
  </AbsoluteFill>
);

export const Root: React.FC = () => (
  <>
    <Composition id="Launch" component={Launch} durationInFrames={total} fps={FPS} width={1920} height={1080} />
    {scenes.map(([Scene, d], i) => (
      <Composition key={i} id={`Scene${i + 1}`} component={Scene} durationInFrames={d} fps={FPS} width={1920} height={1080} />
    ))}
    {/* README images. See README.md in this directory. */}
    <Still id="Banner" component={Banner} width={BANNER_W} height={BANNER_H} />
    <Still id="Social" component={Social} width={SOCIAL_W} height={SOCIAL_H} />
    <Still id="FlowLight" component={Flow} defaultProps={{theme: 'light' as const}} width={FLOW_W} height={FLOW_H} />
    <Still id="FlowDark" component={Flow} defaultProps={{theme: 'dark' as const}} width={FLOW_W} height={FLOW_H} />
  </>
);
