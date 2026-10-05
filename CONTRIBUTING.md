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

The Go core implements the GitHub Action and CLI extension. A dependency-free
Node.js launcher downloads and verifies the exact matching native release.
See [ADR 0001](docs/adr/0001-go-core-js-action-launcher.md) and the
[quota calculation contract](spec/quota-calculation.md).

Use the Go version in `go.mod` and Node.js 24 or newer. No npm dependencies,
transpilation or Action bundle build are needed.

## Validate the GitHub Action launcher

Run without real network access:

```shell
node --check action/launcher.js
node --test action/launcher.test.js
```

The Go Action command is tested with the shared quota implementation below.
Local Action revisions require matching published release assets, so test the
Go command directly during development rather than launching an unpublished
candidate.

## Validate the shared Go implementation

Run:

```shell
go vet ./...
go test -race ./...
go build ./cmd/gh-actions-quota
```

Go source should also be formatted with `gofmt`. Validate all release targets:

```shell
bash scripts/build-release.sh "v$(node -p 'require("./package.json").version')"
node scripts/release.mjs verify-assets "v$(node -p 'require("./package.json").version')" release
```

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
- pass the repository's GitHub Actions checks.

Do not include real GitHub tokens, repository secrets or credentials in tests,
issues or pull requests.

## Commit and release scope

The GitHub Action and CLI extension share one release version.

Do not manually create release tags as part of a normal contribution. Releases
are created through the repository's release workflow.
