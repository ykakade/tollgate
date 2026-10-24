# Contributing to Tollgate

This page tells you how to make a change to Tollgate and how to send it for review.

## Prerequisites

- Go (the version in `go.mod`)
- GNU Make
- [golangci-lint](https://golangci-lint.run) v2
- A C compiler, for the race detector. Without a C compiler, `make test` runs the tests without the race detector.
- [lychee](https://lychee.cli.rs), for `make links`. This is optional. CI also runs the link check.

## Procedure

1. Open an issue before you start a large change. This lets us agree on the design first.
2. Make a branch from `main`.
3. Make the change. Include the code, the tests, the docs, and an entry in `CHANGELOG.md`.
4. Run all checks:

   ```sh
   make check
   ```

5. Open a pull request. Complete the checklist in the pull request template.

Result: CI runs the same checks. A maintainer reviews the pull request.

If a check fails, run `make help` to see the target for that check, and run it alone.

## Make targets

| Target | What it does |
| --- | --- |
| `make build` | Builds `tollgate` and `fakeprovider` into `bin/`. |
| `make test` | Runs the unit tests with the race detector and shows coverage. |
| `make test-integration` | Runs the tests that use the official Anthropic and OpenAI SDKs. |
| `make lint` | Runs golangci-lint on all modules. |
| `make vet` | Runs `go vet` on all modules. |
| `make fmt-check` | Fails if a Go file is not formatted. |
| `make tidy-check` | Fails if a `go.mod` or `go.sum` file is not tidy. |
| `make vuln` | Scans for known vulnerabilities with govulncheck. |
| `make links` | Checks the links in the Markdown files. |
| `make golden` | Writes the golden files of the fake provider again. |
| `make fuzz` | Runs each fuzz target for 30 s. Set `FUZZTIME` to change the time. |
| `make record` | Records provider responses for the golden test. See [testdata/streams](testdata/streams/README.md). |
| `make check` | Runs all local checks except `vuln` and `links`. |

## Commits

- Use [Conventional Commits](https://www.conventionalcommits.org): `feat:`, `fix:`, `docs:`, `test:`, `chore:`.
- Put one logical change in each commit.
- Pull requests merge with squash, so the title of the pull request becomes the commit message.

## Code rules

- Use the standard library first. Before you add a dependency, read the allowed list in the project brief. If the dependency is not on the list, give the reason for it and the alternative in the pull request.
- Make `context.Context` the first parameter of each function that can block.
- Wrap errors with `%w`.
- Log with `log/slog`.
- Do not use package-level variables that change.
- Define small interfaces in the package that uses them.
- Do not call `panic` outside a `main` package.
- Do not commit `TODO` comments or code in comments.
- Give each package a Go doc comment.

## Writing rules

All text for users follows ASD-STE100 Simplified Technical English (STE). This includes the README, the docs, the ADRs, CLI help, HTTP error messages, and log messages. Code comments follow Go doc conventions in plain English.

- Use the words in the [glossary](docs/glossary.md) with their meaning there. Use one word for one meaning.
- Do not use phrasal verbs, such as "set up" or "look up". Use one verb, such as "install" or "find".
- Do not use verb forms that end in "-ing", except in technical names such as "rate limiting".
- Write procedural sentences with 20 words or fewer. Write descriptive sentences with 25 words or fewer.
- Write instructions in the imperative. Put a condition before the instruction: "If the key is not valid, make a new key."
- Use the active voice.
- Put one topic in each paragraph, with 6 sentences or fewer.
- Start a warning with a command, then give the risk.
- Do not use contractions, idioms, humor, or marketing words.
- Put code, paths, flags, headers, and environment variables in backticks.
- Write numbers with units: `30 s`, `16 MiB`, `5 ms`.
- Give each procedure these parts: the goal, the prerequisites, numbered steps, the result, and what to do if it fails.

Messages:

- Go error strings follow Go conventions: lowercase, with no period at the end.
- Messages for people, such as CLI output and HTTP error messages, are full STE sentences. They tell what failed and what to do.
- Log messages are short lowercase STE phrases with no period at the end, for example `request done`. Put the details in attributes with `snake_case` keys.

## Decisions

If your change makes or changes a design decision, write an ADR in `docs/adr/`. Do not change the decision of an accepted ADR. Write a new ADR that supersedes it. See the [ADR index](docs/adr/README.md).

## Code of conduct

This project follows the [code of conduct](CODE_OF_CONDUCT.md).
