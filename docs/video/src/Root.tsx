import React from 'react';
import {AbsoluteFill, Composition, interpolate} from 'remotion';
import {linearTiming, TransitionSeries} from '@remotion/transitions';
import type {TransitionPresentation, TransitionPresentationComponentProps} from '@remotion/transitions';
import {easeInOut} from './components/ui';
import {FPS} from './theme';
import {Hook} from './scenes/Hook';
import {Reveal} from './scenes/Reveal';
import {Failover} from './scenes/Failover';
import {Stat} from './scenes/Stat';
import {Budget} from './scenes/Budget';
import {Dashboard} from './scenes/Dashboard';
import {Features} from './scenes/Features';
import {End} from './scenes/End';

// Zoom-blur transition: the old scene pushes toward the viewer and blurs out,
// the new one settles in from slightly smaller.
const ZoomBlur: React.FC<TransitionPresentationComponentProps<Record<string, never>>> = ({children, presentationDirection, presentationProgress}) => {
  const p = presentationProgress;
  const entering = presentationDirection === 'entering';
  const scale = entering ? interpolate(p, [0, 1], [0.92, 1]) : interpolate(p, [0, 1], [1, 1.12]);
  const blur = entering ? (1 - p) * 18 : p * 18;
  const opacity = entering ? p : 1 - p;
  return <AbsoluteFill style={{transform: `scale(${scale})`, filter: `blur(${blur}px)`, opacity}}>{children}</AbsoluteFill>;
};
const zoomBlur = (): TransitionPresentation<Record<string, never>> => ({component: ZoomBlur, props: {}});

const T = 20;
const scenes: [React.FC, number][] = [
  [Hook, 210],
  [Reveal, 190],
  [Failover, 470],
  [Stat, 170],
  [Budget, 340],
  [Dashboard, 430],
  [Features, 230],
  [End, 300],
];
const total = scenes.reduce((s, [, d]) => s + d, 0) - (scenes.length - 1) * T;

const Launch: React.FC = () => (
  <TransitionSeries>
    {scenes.flatMap(([Scene, d], i) => [
      <TransitionSeries.Sequence key={`s${i}`} durationInFrames={d}>
        <Scene />
      </TransitionSeries.Sequence>,
      ...(i < scenes.length - 1
        ? [<TransitionSeries.Transition key={`t${i}`} presentation={zoomBlur()} timing={linearTiming({durationInFrames: T, easing: easeInOut})} />]
        : []),
    ])}
  </TransitionSeries>
);

export const Root: React.FC = () => (
  <>
    <Composition id="Launch" component={Launch} durationInFrames={total} fps={FPS} width={1920} height={1080} />
    {scenes.map(([Scene, d], i) => (
      <Composition key={i} id={`Scene${i + 1}`} component={Scene} durationInFrames={d} fps={FPS} width={1920} height={1080} />
    ))}
  </>
);
