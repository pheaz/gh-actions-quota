# github-actions-quota

`github-actions-quota` is a public GitHub Action that lets expensive CI jobs run only while the repository owner's included GitHub Actions quota is below a chosen threshold.

The action is read-only. It does not disable workflows, modify repositories, or require repository write permissions.

## What it looks like

```yaml
name: CI

on:
  push:
  pull_request:

jobs:
  quota:
    runs-on: ubuntu-latest
    outputs:
      allowed: ${{ steps.quota.outputs.allowed }}
    steps:
      - uses: philippwallrafen/github-actions-quota@v1
        id: quota
        with:
          token: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
          threshold: 50

  expensive-ci:
    needs: quota
    if: needs.quota.outputs.allowed == 'true'
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v6
      - run: swift test
```

At 49.9% usage, `allowed` is `true`. At 50% or above, it is `false` and the gated job is skipped.

The repository owner is detected automatically from `GITHUB_REPOSITORY_OWNER`. The actor who pushed or opened a pull request does not affect whose quota is measured.

## One-time setup

Run this from the repository that will use the action:

```shell
npx github-actions-quota setup
```

The CLI:

1. Detects the current GitHub repository with `gh`.
2. Starts the GitHub App device authorization flow.
3. Opens GitHub and asks the owner to authorize read-only account-plan access.
4. Receives a non-expiring GitHub App user access token.
5. Validates that the token belongs to the repository owner and can read Actions billing usage.
6. Pipes the token directly into `gh secret set ACTIONS_QUOTA_TOKEN` for the current repository.

The token is never written to a local file by `github-actions-quota`.

`gh` must already be installed and authenticated because the setup command uses the user's existing local GitHub login only to write the repository secret. The long-lived `ACTIONS_QUOTA_TOKEN` itself has only the GitHub App's read-only account permissions.

### GitHub App

The setup CLI uses the public **actions-quota** GitHub App, configured with:

- **Account permission:** Plan — read-only
- **Device Flow:** enabled
- **User access token expiration:** disabled

Its public Client ID is embedded in `src/constants.js`. No client secret is needed by the device flow and no GitHub App secret is committed to this repository.

For development, the client ID can be overridden without modifying source:

```shell
GITHUB_ACTIONS_QUOTA_CLIENT_ID=Iv1.example \
  node src/cli.js setup
```

Automatic setup currently targets repositories owned by personal GitHub accounts. The billing library keeps the user/organization endpoint split, but organization authorization needs separate organization permissions and is intentionally not requested by the v1 GitHub App.

## How quota is measured

GitHub's billing API reports discounts in dollars even when included Actions usage came from runners with different minute prices. The action converts the Actions `discountAmount` back to Linux 2-core equivalent included minutes at `$0.006/min`.

That preserves GitHub's runner multipliers: a macOS minute consumes more of the included allowance than a standard Linux minute.

The included monthly allowance is detected from the account plan:

| Plan | Included Actions minutes |
| --- | ---: |
| GitHub Free | 2,000 |
| GitHub Pro | 3,000 |
| GitHub Free for organizations | 2,000 |
| GitHub Team | 3,000 |
| GitHub Enterprise Cloud | 50,000 |

For unusual or legacy plans, pass an explicit override:

```yaml
with:
  token: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
  threshold: 50
  quota-minutes: 3000
```

## Outputs

The action exposes:

- `allowed` — `true` below the threshold, otherwise `false`
- `usage-available` — whether billing usage was successfully read
- `used-minutes` — Linux-equivalent included minutes consumed
- `quota-minutes` — detected or configured allowance
- `remaining-minutes` — included minutes remaining
- `usage-percent` — percent of the included allowance consumed
- `billing-owner` — repository owner whose quota is charged
- `billing-owner-type` — `user` or `organization`
- `unmetered` — `true` for public repositories using the normal public-repository path

Billing/API/authentication failures are **fail-closed**: the action sets `allowed=false` instead of allowing an expensive job to run with unknown quota state.

## Public repositories

For public repositories, standard GitHub-hosted runners are unmetered. The action therefore returns `allowed=true` without requiring `ACTIONS_QUOTA_TOKEN` when the workflow event identifies the repository as public.

Larger runners are separately billed and are outside this shortcut.

## Important cost detail

A quota gate cannot prevent the gate itself from starting. In a private repository, the small `quota` job still consumes its Ubuntu runner time. Put the quota check in a cheap Linux job and gate expensive macOS/Windows jobs behind it.

If the requirement is literally zero automatic Actions execution after a threshold, a separate external watcher must disable the workflow. That is intentionally outside this action's read-only design.

## Local development

Requires Node.js 20 or newer.

```shell
npm ci
npm test
npm run check
```

There are no runtime npm dependencies.

## Publishing

`publish.yml` uses npm trusted publishing (OIDC), so normal releases do not need a long-lived npm publish token. The npm package must be created once and configured to trust this repository's `publish.yml` workflow before automated publishing can start.

Release tags should expose a stable major tag for Actions consumers, for example `v1` pointing at the current `v1.x.x` release.
