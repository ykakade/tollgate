// Joins PNG frames into an animated WebP for the README.
// Usage: node scripts/webp.mjs <frame dir> <fps> <out.webp> [quality]
import {readdirSync} from 'node:fs';
import {join} from 'node:path';
import sharp from 'sharp';

const [dir, fps, out, quality = '72'] = process.argv.slice(2);
if (!dir || !fps || !out) {
  console.error('Usage: node scripts/webp.mjs <frame dir> <fps> <out.webp> [quality]');
  process.exit(2);
}
const frames = readdirSync(dir).filter((f) => f.endsWith('.png')).sort().map((f) => join(dir, f));
const delay = Math.round(1000 / Number(fps));
const info = await sharp(frames, {join: {animated: true}, limitInputPixels: false})
  .webp({quality: Number(quality), effort: 6, loop: 0, delay: frames.map(() => delay), smartSubsample: true, smartDeblock: true})
  .toFile(out);
console.log(`Wrote ${out}: ${frames.length} frames, ${(info.size / 1e6).toFixed(1)} MB`);
