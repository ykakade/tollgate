# ADR 0031: Mask JSON strings with request-local placeholders

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

Prompts can contain personal data that the operator does not want sent to the provider.

## Decision

Offer tenant-level email, credit card, IPv4 and US SSN detectors and custom regular expressions. Replace matches in JSON strings before forwarding. Keep the original-to-placeholder mapping only for that request. Restore recognized placeholders in successful JSON bodies and supported text stream events. Keep response-cache keys based on the original body.

## Alternatives

A full-body decode and encode changes unrelated JSON. Provider-side masking sends the original data upstream first. Persisted mappings retain sensitive data unnecessarily.

## Consequences

Pattern matching is incomplete. Custom patterns can affect structural strings. Model changes to placeholders prevent restoration. Large non-stream bodies retain placeholders. The feature is opt-in and does not replace a data handling policy.
