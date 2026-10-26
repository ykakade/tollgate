import {loadFont as loadInter} from '@remotion/google-fonts/Inter';
import {loadFont as loadMono} from '@remotion/google-fonts/JetBrainsMono';

const inter = loadInter('normal', {weights: ['400', '500', '600', '700', '800'], subsets: ['latin']});
const mono = loadMono('normal', {weights: ['400', '500', '700'], subsets: ['latin']});

export const font = inter.fontFamily;
export const monoFont = mono.fontFamily;

// Colors from the dashboard (web/src/styles.css), dark mode.
export const c = {
  bg: '#08090b',
  page: '#0d0d0d',
  surface: '#1a1a19',
  raised: '#222220',
  border: 'rgba(255,255,255,0.10)',
  text: '#f4f4f2',
  muted: '#898781',
  blue: '#2a78d6',
  blueLight: '#6da7ec',
  good: '#22c55e',
  warning: '#fab219',
  critical: '#e5484d',
};

export const FPS = 60;
