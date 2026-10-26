# Launch video

The source of `docs/assets/tollgate.mp4` and `docs/assets/tollgate.webp`. The video is made with [Remotion](https://www.remotion.dev): each scene is a React component in `src/scenes/`.

All the product content is real:

- The log lines in the failover scene are from Tollgate with a fake primary provider that returns status 529 (the setup of `docs/demo/record.sh`), filtered with `jq`.
- The 402 error and the budget configuration are from Tollgate and `examples/claude-code/tollgate.yaml`.
- The dashboard images in `public/` are screenshots of `make dashboard-demo`, which uses synthetic data.

Prerequisites:

- Node.js 18 or later
- Remotion downloads a headless Chrome on the first render. To use a Chrome that you have, add `--browser-executable <path>`.

Remotion is free for individuals and for companies with up to 3 employees. See the [Remotion license](https://www.remotion.dev/license).

## Edit the video

```sh
cd docs/video
npm ci
npm run studio
```

Result: Remotion Studio opens in the browser. `Launch` is the full video, and `Scene1` to `Scene8` are the scenes.

## Render the video

1. Render the MP4 (1920×1080, 60 fps):

   ```sh
   npm run render
   ```

2. Render the 1920×1080 animated WebP for the README. GitHub does not autoplay video in a README, but it shows an animated WebP like an image. With `"still": true`, the background does not move and has no film grain, so that the frames compress well:

   ```sh
   npx remotion render Launch out/web-src.mp4 --props='{"still":true}' --crf=4
   mkdir -p out/frames-1080p
   npx remotion ffmpeg -i out/web-src.mp4 -r 20 -vf scale=1920:1080:flags=lanczos out/frames-1080p/f%04d.png
   node scripts/webp.mjs out/frames-1080p 20 out/tollgate.webp 80 4
   ```

   The last argument sets WebP encoding effort (0–6). Effort 4 keeps the 1080p export practical; higher values can make a smaller file but take longer.

3. Copy `out/tollgate.mp4` and `out/tollgate.webp` to `docs/assets/`.

To refresh the dashboard images, run `make dashboard-demo` and take screenshots of `http://127.0.0.1:9090` in dark mode with a 1540 px wide window at 2x scale.
