# Changelog

All important changes to Tollgate are in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The `tollgate version` command. It shows the version, the commit, and the Go version of the binary.
- The `fakeprovider` test server. It speaks the Anthropic Messages API and the OpenAI Chat Completions API wire formats, with streaming and without streaming.
- Tests that use the official Anthropic and OpenAI Go SDKs to read the output of the fake provider.
- The decision records ADR 0001 to ADR 0015, and the glossary.
- The build, test, and lint targets in the `Makefile`, and the CI workflow.
- The contribution guide, the security policy, the code of conduct, and the issue and pull request templates.
