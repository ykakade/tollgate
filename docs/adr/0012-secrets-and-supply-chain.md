# ADR 0012: Protect keys and the build pipeline

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

A gateway holds every provider key, so it is an important target.
In March 2026, malicious LiteLLM versions on PyPI stole credentials after a compromised CI tool leaked a publish token.

## Decision

Make virtual keys from 32 random bytes from `crypto/rand`. Store only their SHA-256 hash.
Read provider keys only from `env:` or `file:` references. Refuse keys in plain text in the configuration.
Pin CI actions by commit SHA. Run govulncheck on each pull request. Sign releases and publish an SBOM with each release.

## Alternatives

1. bcrypt or Argon2 for virtual keys. These are necessary for passwords, but not for random 32-byte keys, and they add CPU time to each request.
2. Provider keys in the configuration file. This is easy, but the keys can then go into version control.

## Consequences

- A lost virtual key cannot be shown again. The user must make a new key.
- Each new dependency and each new CI action needs a review.
