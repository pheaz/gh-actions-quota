# gh-actions-quota

One repository ships a **GitHub Action** to gate expensive CI jobs and a native
**GitHub CLI extension** to configure its billing token. Both share the same
release version.

## Setup

Install [GitHub CLI](https://cli.github.com/) and authenticate with
`gh auth login`. Then run these commands from the repository that will use the action:

```shell
gh extension install philippwallrafen/gh-actions-quota
gh actions-quota setup
```

Setup opens GitHub's device authorization page and displays a code. When a
supported clipboard command is available, the device code is copied to the
clipboard before the browser opens; otherwise setup simply prints the code.
Authorize as the **personal account that owns the current repository**. The
extension checks the account identity, plan and billing access, then saves the
token as the repository secret **`ACTIONS_QUOTA_TOKEN`** using your existing local
`gh` login.

Setup also creates **`.github/workflows/gh-actions-quota.yml`**, a reusable
workflow that wraps the quota action with a default threshold of 50 percent.
Re-running setup leaves an identical generated file unchanged; if that path
contains a modified workflow, setup refuses to overwrite it.

Setup also scans `.github/workflows/*.yml` and
`.github/workflows/*.yaml` (excluding the generated helper) and shows the files
in an interactive checklist. A leading `*` means that workflow currently has a
quota caller. Use Up/Down to move, Space to toggle the `*`, and Enter to apply.
Selecting a file adds the canonical caller with `secrets: inherit`; clearing an
existing `*` removes that caller job again. A pre-existing different
`jobs.quota` is never overwritten.

After workflow selection, setup asks **`Add quota conditions to individual jobs?
[y/n]`**. Press `y` or `n` without Enter. Choosing `y` opens a grouped checklist:
filenames are headings, with job IDs indented beneath them. Only jobs from the
selected workflows appear; quota callers are excluded. Use Up/Down, Space and
Enter as above. Long lists scroll, keeping the current filename and controls
visible. Ctrl-C cancels this step while preserving the completed workflow
selection.

Selecting a job adds the quota dependency and an active condition with an
editable **50% threshold directly in that job's workflow file**:

```yaml
  build:
    needs: quota
    # Quota threshold (%): change 50 below to adjust this job's limit.
    if: ${{ needs.quota.outputs.usage_available == 'true' && fromJSON(needs.quota.outputs.usage_percent) < 50 }}
    runs-on: ubuntu-latest
```

Change `50` in each job's condition to adjust its limit. Different jobs in the
same workflow can use different thresholds. Jobs compare reported usage directly
with their own limits, so a 75% job can run even when the reusable helper's
50% `allowed` output is false. Exactly at or above the job's threshold, or when
usage is unavailable, the job is skipped.

Setup preserves existing dependencies and combines existing conditions with the
quota condition using a parenthesized AND. Recognizable quota gates start
selected; clearing them removes the quota gate and dependency while retaining
unrelated conditions and dependencies. Re-running setup preserves edited job
thresholds. Existing canonical `allowed` gates migrate using the caller's literal
threshold, or 50% when no override exists. Dynamic thresholds, complex custom
quota expressions, and YAML constructs that cannot be safely edited appear
as disabled entries with a manual-editing explanation.

Setup lists the configured workflow files and reminds you that thresholds can
be adjusted there. Jobs that you leave unselected must be configured manually
if you want them to be gated.

The project never saves the token to a local file, passes it in command arguments
or prints it. It remains in memory and is piped to `gh secret set` through stdin.
There is no server, central token store or telemetry. `gh` is only needed for
setup; workflow users need no additional runtime or installation step.

Organization-owned repositories are **not supported for metered billing in v1**.
Public repositories using standard GitHub-hosted runners need no setup or token.

## Workflow

```yaml
name: CI

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit

  expensive-ci:
    needs: quota
    # Quota threshold (%): change 50 below to adjust this job's limit.
    if: ${{ needs.quota.outputs.usage_available == 'true' && fromJSON(needs.quota.outputs.usage_percent) < 50 }}
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v6
      - run: swift test
```

The generated reusable workflow runs its quota check on `ubuntu-slim` and
defaults to a 50 percent threshold. Override it on the reusable-workflow call
with `with: { threshold: 75 }` when needed.
Usage below the threshold gives `allowed=true`. **Exactly at the threshold or
above it, `allowed=false`.** Billing
belongs to `GITHUB_REPOSITORY_OWNER`, never the actor or pull request author.

Authentication, billing, API or invalid-input failures fail closed: the action
emits a warning, sets `usage-available=false` and `allowed=false`, and finishes
successfully so gated jobs are skipped. Pull requests without access to the
repository secret also fail closed in private repositories.

## GitHub App and security

The **gh-actions-quota** GitHub App uses:

| Setting | Value |
| --- | --- |
| Public Client ID | `Iv23liXk29OIBFBTJjap` |
| Account permission | **Plan: Read-only** |
| Repository permissions | None |
| Organization permissions | None in v1 |
| Device Flow | Enabled |
| User-to-server token expiration | Disabled |
| Client secret | Not used |
| Homepage | https://github.com/philippwallrafen/gh-actions-quota |

The app token only reads the personal account's plan and billing. The app does
not request `Secrets: write`. Only the locally authenticated `gh` process writes
the repository secret. The action subsequently reads that secret; it does not
write repository settings. Expiring tokens and refresh-token responses are
rejected because this design does not store or refresh credentials locally.

Re-run setup to replace a revoked token. Authorization can be revoked in your
GitHub account's authorized GitHub Apps settings.

See GitHub's [device-flow documentation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
and [billing endpoint permissions](https://docs.github.com/en/rest/billing/usage).

## Quota calculation

The action reads the current **UTC calendar month's** Actions billing summary
for the repository owner's account. It sums `discountAmount` only for
`product=Actions` and `unitType=minutes`, then divides by **`$0.006/min`** to
calculate Linux-equivalent included minutes. Other products and storage usage
are excluded. Usage is account-wide, including other repositories owned by the
same account. Billing data may arrive with a delay, so this is a gate based on
reported usage rather than a real-time spending limit.

| Plan | Included monthly minutes |
| --- | ---: |
| Free | 2,000 |
| Pro | 3,000 |
| Team | 3,000 |
| Enterprise Cloud | 50,000 |

Team and Enterprise mappings are retained; organization billing is not enabled
by those mappings in v1. For an unusual personal plan, override the allowance
explicitly:

```yaml
with:
  token: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
  threshold: 50
  quota-minutes: 3000
```

`token` also falls back to the `ACTIONS_QUOTA_TOKEN` environment variable.

## Outputs

| Output | Meaning |
| --- | --- |
| `allowed` | `true` below the threshold; `false` at/above it or on failure |
| `usage-available` | Whether usage was determined successfully |
| `used-minutes` | Linux-equivalent included minutes consumed |
| `quota-minutes` | Detected or overridden monthly allowance |
| `remaining-minutes` | Remaining minutes, with a minimum of zero |
| `usage-percent` | Percent of included allowance consumed |
| `billing-owner` | Repository owner whose quota is charged |
| `billing-owner-type` | `user`; `unmetered` for public repos; `unavailable` on failure |
| `unmetered` | `true` for the public-repository shortcut |

For public repositories, standard GitHub-hosted runners are unmetered:
`allowed=true`, `usage-available=true`, `used-minutes=0`, `usage-percent=0`,
`quota-minutes=unmetered` and `remaining-minutes=unmetered`. Larger runners are
separately billed and are outside this shortcut; do not use it as a gate for
larger-runner spending.

In private repositories, the small quota job **consumes runner time itself**.
The generated helper uses `ubuntu-slim` to keep that gate lightweight. The
action cannot prevent its own job from starting.

## Development

The action uses strict TypeScript and esbuild, targeting Node.js 24. Its complete
bundle is committed at `dist/index.js`; `action.yml` runs it with `node24`.
Node.js and the JavaScript package manager are development/build tools only.
The extension uses Go 1.27.1 or newer. It uses `golang.org/x/term` for the
cross-platform interactive workflow and job checklists, and `go.yaml.in/yaml/v3`
for structural validation and targeted YAML edits.

After installing the locked development dependencies, validate the action with:

```shell
node --run typecheck
node --run test
node --run build
git diff --exit-code -- dist/
```

Validate and build the extension:

```shell
go vet ./...
go test -race ./...
go build ./cmd/gh-actions-quota
bash scripts/build-release.sh v1.0.0
```

For local extension testing, build the executable at the repository root:

```shell
go build -o gh-actions-quota ./cmd/gh-actions-quota
gh extension install .
```

On Windows, name the local executable `gh-actions-quota.exe`. Tests use mocked
GitHub responses and deliberately fake tokens; they do not authorize the app or
write real repository secrets.

## Releases

`v1.0.0` versions the action and extension together. The tag contains TypeScript
source, `dist/index.js`, `action.yml` and Go source. The GitHub Release contains
five standalone binaries and `checksums.txt`:

- `darwin/arm64`
- `darwin/amd64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`

Asset names end in the platform suffix expected by `gh extension install`, for
example `gh-actions-quota_v1.0.0_darwin-arm64` and
`gh-actions-quota_v1.0.0_windows-amd64.exe`. There are no archives or runtime
dependencies for users.

Before tagging, update the shared version in `package.json` and its lockfile,
rebuild and commit `dist/index.js`, then push a matching semantic version tag.
The release workflow repeats TypeScript checks, verifies the committed bundle,
tests Go on Linux/macOS/Windows, cross-compiles the binaries, generates SHA256
checksums and publishes assets with **`GITHUB_TOKEN`**. No long-term publishing
credential is required.

After a successful stable v1 release, the workflow advances the moving **`v1`**
tag to the same commit. Prereleases do not advance it, and retries of older tags
cannot move it backwards. Repository rules must allow the release workflow to
create semantic version tags and update `v1`.

Maintainers must configure the GitHub App settings above before the first public
release. App registration settings are independent of repository files.
