# Feature video

Source for the 81-second feature tour in [`docs/assets/tollgate.mp4`](../assets/tollgate.mp4) and the animated [`tollgate.webp`](../assets/tollgate.webp). Each scene is a React component rendered with [Remotion](https://www.remotion.dev).

## Story and sources

The opening identifies the product and its main controls within three seconds. The tour then explains each feature with diagrams, configuration examples, logs, and dashboard views.

| Start | Scene | Content |
| --- | --- | --- |
| 0:00 | Introduction | Current logo, product name, and three concrete capabilities |
| 0:04 | Architecture | Clients, the gateway, and native provider APIs |
| 0:10 | Request pipeline | Authentication, preflight, limits, cache, forward, and settlement |
| 0:26 | Failover | A primary returns 529; a backup returns a successful stream |
| 0:33 | Resilience | Circuit breaker states and retry rules |
| 0:41 | Budgets | A hard budget refuses a request with HTTP 402 |
| 0:46 | Agent sessions | Sticky targets, personal data redaction, and session budgets |
| 0:54 | Observability | A schematic trace and the gateway's metric names |
| 1:00 | Dashboard | Overview, session details, and provider health |
| 1:11 | Engineering | Tests, coverage, SDK checks, and architecture decisions |
| 1:16 | Install | Install command and repository address |

The scenes are animated explanations, not a continuous screen recording. Failover logs and the 402 response reproduce local Tollgate output with fake providers. Configuration and protocol names follow the repository guides. The trace uses illustrative span placement; its bars do not use a uniform time scale.

Dashboard images come from `make dashboard-demo` and use synthetic data. The sticky-routing cost comparison comes from the [synthetic replay](../agent-features.md#replay-results).

The displayed **89 µs** is the overhead of one local demo request, not a general latency guarantee. Repository statistics reflect the code at render time: 250 Go tests, 90.8% statement coverage, 33 ADRs, and four fuzz targets.

## Edit

Goal: change a scene and inspect its composition.

Prerequisites: Node.js 20 or later and npm. The first render downloads headless Chrome. Allow several GB of disk space for PNG frames.

1. Install dependencies:

   ```sh
   cd docs/video
   npm ci
   ```

2. Open Remotion Studio:

   ```sh
   npm run studio
   ```

3. Select `Launch` for the complete video, or `Scene1` through `Scene11` for an individual scene.
4. After edits, run `npm run typecheck`.

Result: the compositions show the current source. If Chrome cannot start, pass `--browser-executable <path>` to the Remotion command.

Remotion has its own [license terms](https://www.remotion.dev/license). Review them before commercial use.

## Export

Goal: produce a sharp 1920×1080 MP4 and animated WebP.

Prerequisites: the dependencies above.

1. Render and encode both formats:

   ```sh
   npm run render
   ```

   The MP4 is 60 fps. The WebP is 20 fps at the same 1920×1080 resolution. Both use the PNG frame sequence in `out/launch-frames/`.

2. To repeat only an encoding step:

   ```sh
   npm run encode:mp4
   npm run encode:webp
   ```

3. Copy the exports and poster:

   ```sh
   cp out/tollgate.mp4 out/tollgate.webp ../assets/
   cp out/launch-frames/element-0150.png ../assets/video-poster.png
   ```

Result: the README and documentation site use the refreshed assets. If the render fails, inspect the Remotion error, correct the source, and repeat step 1.

The WebP encoder reads PNGs directly. An intermediate H.264 video would reduce color detail around small text. Mixed WebP encoding preserves flat UI regions, while a fixed background avoids noise and excessive file size. Larger text and focused screenshot views help at GitHub's narrower display width.

The `still` input prop fixes the background and removes grain. It does not stop scene animations. The output directories are ignored by Git.

## Refresh screenshots

Goal: capture current dashboard screens with the current logo.

Prerequisites: a browser that can capture at 2× device scale.

1. From the repository root, run `make dashboard-demo`.
2. Open `http://127.0.0.1:9090` with a 1540 CSS-pixel viewport at 2× scale.
3. Capture Overview, Sessions, a session detail, Providers, and Keys in both themes. Crop only empty space below the page content.
4. Save the captures as `docs/assets/dashboard-{page}-{theme}.png`.
5. Copy the dark Overview, session detail, and Providers images into `public/overview.png`, `public/session.png`, and `public/providers.png`.
6. Render the tour again, then inspect the dashboard camera at each focus point.

Result: the README, guides, and video show the same product version. If session data is sparse, wait for the synthetic traffic before capture.
