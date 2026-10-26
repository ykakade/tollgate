// Joins PNG frames into an animated WebP for the README.
// Usage: node scripts/webp.mjs <frame dir> <fps> <out.webp> [quality] [effort] [step]
//
// The frames come straight from Remotion as PNG, so text keeps full color
// detail (an H.264 step halves the color resolution and blurs colored text).
// `step` keeps every nth frame, for example 2 to make 30 fps from 60 fps.
// The encoder can use lossless or lossy coding for each frame ("mixed"), so
// that frames of flat UI and text stay sharp.
import {readdirSync} from 'node:fs';
import {join} from 'node:path';
import sharp from 'sharp';

const [dir, fps, out, quality = '90', effort = '4', step = '1'] = process.argv.slice(2);
if (!dir || !fps || !out) {
  console.error('Usage: node scripts/webp.mjs <frame dir> <fps> <out.webp> [quality] [effort] [step]');
  process.exit(2);
}
const frames = readdirSync(dir)
  .filter((f) => f.endsWith('.png'))
  .sort()
  .filter((_, i) => i % Number(step) === 0)
  .map((f) => join(dir, f));
const delay = Math.round(1000 / Number(fps));
const info = await sharp(frames, {join: {animated: true}, limitInputPixels: false})
  .webp({quality: Number(quality), effort: Number(effort), loop: 0, delay: frames.map(() => delay), smartSubsample: true, mixed: true, minSize: false})
  .toFile(out);
console.log(`Wrote ${out}: ${frames.length} frames, ${(info.size / 1e6).toFixed(1)} MB`);
