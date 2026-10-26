---
layout: home

hero:
  name: Tollgate
  text: The AI gateway for coding agents
  tagline: One Go binary that controls cost, rate limits and failover for the Anthropic and OpenAI APIs.
  image:
    src: /favicon.svg
    alt: Tollgate logo
  actions:
    - theme: brand
      text: Quick start
      link: /quickstart
    - theme: alt
      text: Configuration
      link: /configuration
    - theme: alt
      text: View on GitHub
      link: https://github.com/ykakade/tollgate

features:
  - title: Hard budgets
    details: Set a budget for each tenant, each month and each agent session. A hard budget refuses the request with status 402 before the provider gets it.
    link: /limits
    linkText: Rate limits and budgets
  - title: Failover before the first byte
    details: Ordered targets, retries, a retry budget and circuit breakers. Tollgate moves to the next target before the client sees an error.
    link: /reliability
    linkText: Reliability
  - title: Built for agent sessions
    details: Session budgets, sticky routing that helps preserve the warm prompt cache, an exact response cache and personal data redaction.
    link: /agent-features
    linkText: Agent features
  - title: Metrics, traces and cost
    details: Prometheus metrics, OpenTelemetry traces with one span for each attempt, and one JSONL usage event with the cost of each request.
    link: /observability
    linkText: Observability
  - title: Dashboard in the binary
    details: Overview, sessions, providers and keys, in light and dark mode. The admin port serves it. No separate process is necessary.
    link: /admin-api
    linkText: Admin API and dashboard
  - title: Native API formats
    details: Anthropic /v1/messages and OpenAI /v1/chat/completions keep their format. Streams pass through byte for byte, unless redaction or a budget cutoff changes them.
    link: /configuration
    linkText: Configuration
---

<div class="home-section vp-doc">

## See it work

<video class="demo-video" src="./assets/tollgate.mp4" poster="./assets/video-poster.png" aria-label="Tollgate feature tour" controls muted playsinline preload="none"></video>

The 81-second tour explains request controls, failover, circuit breakers, budgets, sessions, traces, and the dashboard. Animated examples use Tollgate behavior and local fake-provider output. Screenshots use synthetic data. See [how the video is made](video/).

## Start in one command

For a local dashboard with synthetic traffic and no API key, clone the repository and run:

```sh
make dashboard-demo
```

Open `http://127.0.0.1:9090`. You need Go, GNU Make, and Node.js. See the [quick start](quickstart.md) for a first API request.

## Install

```sh
go install github.com/ykakade/tollgate/cmd/tollgate@latest
```

Release binaries for Linux are on the [releases page](https://github.com/ykakade/tollgate/releases/latest). Continue with the [quick start](quickstart.md).

## The dashboard

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./assets/dashboard-overview-dark.png">
  <img src="./assets/dashboard-overview-light.png" alt="Tollgate dashboard with spend, request totals, cache use, and route costs">
</picture>

The screenshot uses synthetic demo data. Run `make dashboard-demo` to show it on your computer.

</div>
