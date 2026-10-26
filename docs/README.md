# Tollgate documentation

Tollgate controls cost, rate limits, and provider failover for coding agents. This directory contains the guides and the searchable documentation site.

## Start here

| Goal | Read |
| --- | --- |
| Install Tollgate and send a first request | [Quick start](quickstart.md) |
| Connect Claude Code | [Claude Code example](../examples/claude-code/README.md) |
| Configure providers, routes, and keys | [Configuration](configuration.md) |
| Set budgets and understand their tolerances | [Rate limits and budgets](limits.md) |
| Handle provider failures | [Reliability](reliability.md) |
| Use sessions, caches, and redaction | [Agent features](agent-features.md) |
| Inspect the dashboard or call its API | [Admin API and dashboard](admin-api.md) |
| Export metrics, traces, and usage | [Observability](observability.md) |
| Operate the gateway | [Operations](operations.md) |

## Reference and design

- [Architecture decision records](adr/README.md): 33 decisions, their alternatives, and consequences.
- [Glossary](glossary.md): terms used by the guides.
- [Validation](m1-validation.md): acceptance checks and known limits.
- [Terminal demo](demo/README.md): reproduce the failover recording.
- [Feature video](video/README.md): scenes, exports, and media reproduction.
- [Changelog](../CHANGELOG.md): release history.

## Read the site locally

Goal: read the guides with search, navigation, and light or dark mode.

Prerequisites: Node.js 20 or later, npm, and GNU Make.

1. From the repository root, run:

   ```sh
   make docs-serve
   ```

2. Open the address printed by VitePress. The default is `http://localhost:5173/tollgate/`.

Result: the documentation site opens. Stop the server with `Ctrl-C`.

If the default port is busy, VitePress prints another port. If a dependency fails to install, check Node.js and run the command again.

## Build and publish

Goal: produce a static site for GitHub Pages.

Prerequisites: the tools above and a repository with GitHub Pages enabled for GitHub Actions.

1. Run `make docs`. The build checks internal page links and writes `docs/.vitepress/dist/`.
2. In repository Settings → Pages, select **GitHub Actions** as the source.
3. Push the changes to `main`, or run the **Documentation** workflow manually.

Result: the workflow publishes the site at `https://ykakade.github.io/tollgate/`. Pull requests build the site without deployment.

If deployment fails, inspect the workflow log and confirm the Pages source. The repository does not need a separate documentation branch.

The site uses the same Markdown files as GitHub. Edit a guide here, then run `make docs` before a pull request.
