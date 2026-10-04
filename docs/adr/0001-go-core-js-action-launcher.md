# ADR 0001: Go core with a minimal JavaScript Action launcher

- Status: Accepted
- Date: 2026-10-04

## Context

gh-actions-quota currently has two product implementations:

- a TypeScript GitHub Action running on Node.js
- a native Go CLI / gh extension

Both implementations contain overlapping account, billing, quota, and usage logic. This creates avoidable duplication in the most important domain code and makes behavior drift between the Action and CLI possible.

GitHub Actions does not provide a native `using: go` runtime. A Go implementation therefore still needs a small Action runtime layer that starts the correct native binary.

The Action must remain practical on GitHub-hosted Linux, macOS, and Windows runners.

## Decision

All product and domain logic will live in Go.

The GitHub Action will remain a JavaScript Action with a small, dependency-free JavaScript launcher running on the GitHub-provided Node.js runtime.

The launcher is packaging/runtime infrastructure only. It must not contain billing, quota, plan, repository-visibility, threshold, or GitHub-output business logic.

The target dependency direction is:

```text
action.yml
    |
    v
action/launcher.js
    |
    | download + verify + exec
    v
gh-actions-quota action
    |
    v
internal/action
    |
    v
internal/quota
```

The same Go quota implementation is used by the Action and CLI commands.

## JavaScript launcher responsibilities

The launcher may only:

1. determine the runner OS and architecture;
2. determine the exact gh-actions-quota release version associated with the checked-out Action revision;
3. select the matching release asset;
4. download the native binary and `checksums.txt`;
5. verify the binary SHA-256 checksum;
6. make the binary executable where required;
7. execute `gh-actions-quota action` with the current environment and inherited stdio;
8. propagate the child process exit status.

It must use only Node.js built-in APIs. No TypeScript, transpilation, bundling, or runtime npm dependencies are required for the launcher.

The launcher must never download `latest`. Released Action revisions must resolve to the exact matching release tag/version so Action source and native binary cannot silently drift apart.

For the current release tooling, the exact package version may remain the version source as long as release automation keeps it in lockstep with the Action revision. A dedicated version file may replace it later without changing this architecture.

## Go responsibilities

Go owns all application behavior, including:

- GitHub API requests;
- billing usage parsing;
- SKU normalization and quota calculation;
- plan and included-minute handling;
- repository visibility handling;
- threshold evaluation;
- fail-closed behavior;
- GitHub Action inputs and outputs;
- step-summary generation;
- CLI commands and presentation.

The Action-specific Go entry point should be exposed as a command such as:

```text
gh-actions-quota action
```

Shared quota behavior belongs in a package such as `internal/quota`; Action-specific environment/output adaptation belongs in `internal/action`.

## Why not a Composite Action

A Composite Action is valid, but it is not preferred here.

Composite Actions are best suited to declaratively composing existing Actions and shell steps. This project instead needs a small cross-platform bootstrap program that performs platform detection, release-asset selection, download, checksum verification, and native process execution.

Implementing that bootstrap in Composite YAML would either:

- duplicate Bash and PowerShell logic across Unix and Windows; or
- depend on shell behavior and external command availability.

A small JavaScript launcher uses the same Node.js APIs on all supported GitHub-hosted runners and is easier to unit test.

## Why not keep the TypeScript Action

TypeScript is not needed for product logic once Go is the single implementation.

Keeping a full TypeScript Action would preserve duplicate domain behavior and two independent implementations of billing/quota rules.

A TypeScript-only launcher would also add compiler, bundler, type definitions, lockfile churn, and build steps without providing meaningful value over a small plain JavaScript launcher.

## Consequences

### Positive

- One implementation of quota and billing behavior.
- CLI and GitHub Action cannot diverge in domain rules.
- SKU prices, normalization rules, and quota calculations have one source of truth.
- The JavaScript surface becomes small and infrastructure-only.
- No TypeScript compiler or Action bundle is required.
- Cross-platform bootstrap behavior remains straightforward.
- Most Action behavior can be tested directly as Go code.

### Negative

- Released Action executions require downloading a native release asset before running.
- Release assets become part of the Action's runtime contract.
- Release automation must publish binaries for every supported runner platform/architecture.
- Action version and binary release version must remain synchronized.
- Bootstrap failures such as download or checksum errors must be handled explicitly and fail closed.

## Required invariants

- Never resolve or download an unpinned `latest` binary.
- Verify the downloaded binary against the checksum manifest before execution.
- Do not put product/domain logic in `launcher.js`.
- Do not duplicate quota calculation between Action and CLI.
- Keep Action outputs and fail-closed semantics compatible unless intentionally changed and documented.
- CI must test launcher platform mapping and checksum handling.
- CI must test the Go Action command independently of the launcher.

## Migration outline

1. Extract shared billing/quota behavior from the existing Go setup code into `internal/quota`.
2. Add `internal/action` and a `gh-actions-quota action` command.
3. Port the current Action input/output and summary behavior to Go.
4. Add the new quota-calculation specification and tests against the shared Go implementation.
5. Replace the current TypeScript Action with `action/launcher.js`.
6. Change `action.yml` to run the plain JavaScript launcher on the GitHub-provided Node.js runtime.
7. Update CI to test the launcher and Go Action command.
8. Remove TypeScript sources, TypeScript tests, tsconfig files, esbuild, and `dist/index.js`.
9. Keep the existing JavaScript release tooling for now; migrating release automation away from Node.js is a separate decision.

## Out of scope

This ADR does not decide:

- whether the release-management scripts should eventually be rewritten in Go;
- how organization billing support should be added;
- how historical public/private repository visibility should be reconstructed.

Those concerns can be handled independently.
