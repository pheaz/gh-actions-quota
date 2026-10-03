# Security Policy

Security is important for `gh-actions-quota` because the CLI handles GitHub App
user tokens and repository secret configuration.

## Supported versions

Security fixes are provided for the latest stable `v1` release.

Users should upgrade the GitHub CLI extension to the latest available version:

```shell
gh extension upgrade actions-quota
```

GitHub Actions users should reference the current supported major version:

```yaml
uses: philippwallrafen/gh-actions-quota@v1
```

## Reporting a vulnerability

Do **not** report suspected security vulnerabilities in a public GitHub issue.

Use GitHub's private vulnerability reporting for this repository:

**Security -> Advisories -> Report a vulnerability**

Report enough information to reproduce and assess the issue, including:

- affected version;
- operating system where relevant;
- affected command or GitHub Action behavior;
- reproduction steps;
- expected and actual behavior;
- potential security impact.

Do not include real access tokens, repository secrets, credentials or other
sensitive data in a report.

## Credential handling

`gh-actions-quota` does not intentionally store GitHub App tokens in plaintext.

Persistent CLI authorization uses the operating system's secure credential
storage:

- macOS Keychain;
- Windows Credential Manager;
- Linux Secret Service through `secret-tool`.

When secure storage is unavailable, authorization remains in memory only.

Repository token installation passes the token to `gh secret set` through
standard input rather than through command-line arguments.

The CLI must never print the token or include it in command arguments.
