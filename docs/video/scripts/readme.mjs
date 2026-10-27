// Encodes the README stills from out/readme/ into docs/assets/.
// Usage: node scripts/readme.mjs
//
// "npm run readme" renders the PNGs first. The banner keeps its rounded
// corners as alpha. The diagrams are flat colors and text, so lossless WebP
// is smaller than lossy WebP and keeps every edge sharp. GitHub accepts only
// PNG, JPEG, or GIF for the social preview, so it is a JPEG.
import sharp from 'sharp';

const jobs = [
  ['out/readme/banner.png', '../assets/readme-banner.webp', (s) => s.webp({quality: 90, alphaQuality: 100, smartSubsample: true, effort: 6})],
  ['out/readme/flow-light.png', '../assets/readme-flow-light.webp', (s) => s.webp({lossless: true, effort: 6})],
  ['out/readme/flow-dark.png', '../assets/readme-flow-dark.webp', (s) => s.webp({lossless: true, effort: 6})],
  ['out/readme/social.png', '../assets/social-preview.jpg', (s) => s.jpeg({quality: 90, mozjpeg: true})],
];

for (const [src, out, encode] of jobs) {
  const info = await encode(sharp(src)).toFile(out);
  console.log(`Wrote ${out}: ${info.width}×${info.height}, ${Math.round(info.size / 1024)} KiB`);
}
