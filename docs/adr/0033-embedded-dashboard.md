# ADR 0033: Embed the dashboard build in the Go binary

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

The dashboard must work without a separate Node server or CDN.

## Decision

Build a Vite, React and TypeScript app into web/dist and commit the assets. Embed them with go:embed. Serve static files on the admin listener with a content security policy. Use only the same-origin admin API for data. Permit unauthenticated static assets so the app can request an admin token. Keep that token in tab sessionStorage. Check source and dist consistency in CI.

## Alternatives

Require Node for every Go build. This enlarges the ordinary build toolchain. Serve CDN assets. This requires network access and broadens the script trust boundary.

## Consequences

Ordinary Go builds need only committed assets. Frontend changes require make web and a commit of dist. The dashboard has light, dark and system themes. Remote deployments need an HTTPS reverse proxy.
