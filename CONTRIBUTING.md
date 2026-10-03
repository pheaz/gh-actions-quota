# Contributing

Contributions to `gh-actions-quota` are welcome through GitHub issues and pull
requests.

## Before opening a pull request

For bug fixes and small improvements, a pull request can be opened directly.

For larger behavior or architecture changes, consider opening an issue first so
the intended behavior can be discussed before implementation.

Security vulnerabilities must not be reported through public issues. See
[`SECURITY.md`](SECURITY.md).

## Development setup

The repository contains both:

- a TypeScript GitHub Action;
- a Go GitHub CLI extension.

Use the tool versions declared by the repository and install the Node
dependencies with:

```shell
npm ci
```

## Validate the GitHub Action

Run:

```shell
npm run typecheck
npm test
npm run build
git diff --exit-code -- dist/
```

`dist/index.js` is committed and must match the TypeScript source.

## Validate the CLI extension

Run:

```shell
go vet ./...
go test -race ./...
go build ./cmd/gh-actions-quota
```

Go source should also be formatted with `gofmt`.

## Release-management tests

Run:

```shell
node --test scripts/release.test.mjs
```

## Pull requests

Keep pull requests focused on one logical change.

A pull request should:

- explain the user-visible behavior being changed;
- include or update tests for changed behavior;
- update the README when user-facing behavior changes;
- keep credentials and tokens out of logs, argv and fixtures;
- keep `dist/index.js` synchronized when Action source changes;
- pass the repository's GitHub Actions checks.

Do not include real GitHub tokens, repository secrets or credentials in tests,
issues or pull requests.

## Commit and release scope

The GitHub Action and CLI extension share one release version.

Do not manually create release tags as part of a normal contribution. Releases
are created through the repository's release workflow.
