## Summary

Describe the change and why it is needed.

## User-visible behavior

Describe any change to CLI output, Action behavior, workflow generation,
authentication, quota calculation or repository setup.

If there is no user-visible change, state that explicitly.

## Validation

- [ ] `node --check action/launcher.js`
- [ ] `node --test action/launcher.test.js`
- [ ] `gofmt` check
- [ ] `go vet ./...`
- [ ] `go test -race ./...`
- [ ] `go build ./cmd/gh-actions-quota`
- [ ] `node --test scripts/release.test.mjs`

Run only the checks relevant to the change, but explain any intentionally skipped
checks below.

## Security

- [ ] No real tokens, secrets or credentials were added.
- [ ] Tokens are not written to logs or command-line arguments.
- [ ] Authentication or credential-storage changes include appropriate tests.

## Documentation

- [ ] README/documentation was updated when user-facing behavior changed.
