# gh-actions-quota

One repository ships a **GitHub Action** to gate expensive CI jobs and a native
**GitHub CLI extension** to configure its billing token. Both share the same
release version.

## Setup

Install [GitHub CLI](https://cli.github.com/) and authenticate with
`gh auth login`. Then run these commands from the repository that will use the action:

```shell
gh extension install philippwallrafen/gh-actions-quota --force
gh actions-quota setup
```

For a **private repository**, setup first looks for an existing gh-actions-quota
authorization for the owning personal account in the operating system's secure
credential store. If none is available, setup opens GitHub's device authorization
page and copies the device code to the clipboard before opening the browser when
clipboard support is available. Authorize as the **personal account that owns the
current repository**. After validating the account identity, plan and billing
access, the extension stores the App token in the OS credential store and saves
the same token as the repository secret **`ACTIONS_QUOTA_TOKEN`** using your
existing local `gh` login. The cached authorization is account-scoped, not
repository-scoped, so other private repositories owned by the same personal
account can reuse it without another device login.

For a **public repository**, standard GitHub-hosted runners are unmetered. Setup
prints the repository and its visibility, reports that setup is not required,
and exits successfully. It performs no GitHub App authorization or credential
access, creates no secrets or workflow files, and leaves existing workflows
unchanged without opening workflow selection:

```text
Repository: some-org/example
Visibility: Public (unmetered)

Setup is not required for public repositories.
```

For private repositories, setup also creates
**`.github/workflows/gh-actions-quota.yml`**, a reusable workflow that wraps the
quota action with a default threshold of 50 percent.
The Action name, reusable workflow name, job ID/name and step ID are all
`gh-actions-quota`. Re-running setup leaves an identical generated file unchanged
and refuses to overwrite any file that differs from the current template.

Setup also scans `.github/workflows/*.yml` and
`.github/workflows/*.yaml` (excluding the generated helper) and shows the files
in an interactive checklist. A leading `*` means that workflow currently has a
quota caller. Use Up/Down to move, Space to toggle the `*`, and Enter to apply.
Selecting a file adds this canonical caller; clearing an existing `*` removes it:

```yaml
jobs:
  gh-actions-quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit
```

Setup recognizes only this job ID with these two fields. A different
`jobs.gh-actions-quota` produces an error and must be edited manually.
Unrelated jobs, dependencies, conditions and comments are preserved.

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
    needs: gh-actions-quota
    # Quota threshold (%): change 50 below to adjust this job's limit.
    if: ${{ needs.gh-actions-quota.outputs.usage_available == 'true' && fromJSON(needs.gh-actions-quota.outputs.usage_percent) < 50 }}
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
thresholds. Custom quota expressions, including conditions based on `allowed`,
and YAML constructs that cannot be safely edited appear as disabled entries
with a manual-editing explanation.

Setup lists the configured workflow files and reminds you that thresholds can
be adjusted there. Jobs that you leave unselected must be configured manually
if you want them to be gated.

The project never saves the token to a plaintext file, passes it in command
arguments or prints it. For setup and status it persists the token only
in the operating system's secure credential store: macOS Keychain, Windows
Credential Manager, or the Linux Secret Service via `secret-tool`. If secure
storage is unavailable, the command still works with an in-memory token and asks
for authorization again next time; there is no plaintext fallback. Repository
secret writes continue to pipe the token to `gh secret set` through stdin.
There is no server, central token store or telemetry.

Organization-owned repositories are **not supported for metered billing in v1**.
Public repositories using standard GitHub-hosted runners need no workflow setup
or repository token. Status can still show a personal account's private quota
from a public repository, including one owned by an organization.

## Status

Run `gh actions-quota status` from a repository checkout or from any other
directory. When no repository can be resolved, status still shows the current
personal GitHub account's private Actions quota:

```text
Repository: not found

Actions quota:
  Account: philippwallrafen
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

When a repository is available, status also reports whether that repository is
metered and inspects its gh-actions-quota setup.

For a **public repository**, status uses the personal account currently signed in
with `gh` on github.com. Public repositories are unmetered on standard
GitHub-hosted runners. Setup artifacts are shown only when they are present:

```text
Repository: some-org/example
Visibility: Public (unmetered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: philippwallrafen
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

If neither `.github/workflows/gh-actions-quota.yml` nor
`ACTIONS_QUOTA_TOKEN` is present, the `Setup` block is omitted entirely for
public repositories. A present workflow or secret is shown individually; missing
public setup artifacts are never printed.

For a **private repository**, status uses the repository owner's personal account
quota. Organization-owned private repositories remain unsupported. Both setup
states are always shown:

```text
Repository: philippwallrafen/example
Visibility: Private (metered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: philippwallrafen
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

A private repository with incomplete setup reports the missing state explicitly:

```text
Setup:
  Workflow: missing
  Secret:   missing
```

The workflow check only inspects the local
`.github/workflows/gh-actions-quota.yml` path. The secret check lists repository
Actions secret metadata and tests only for the name `ACTIONS_QUOTA_TOKEN`; secret
values are never read or printed. If secret metadata cannot be inspected for a
public repository, no secret status is shown. For a private repository, status
returns an error rather than incorrectly reporting the secret as missing.

Visibility describes the repository. The quota block always describes the
displayed personal account's private Actions usage across its repositories:
the current `gh` account for public repositories or when no repository is found,
and the repository owner for private repositories.

Both public and private status reuse the relevant account's cached GitHub App
authorization. The device flow is shown only when no usable credential exists,
when it was revoked or belongs to the wrong account, or when secure storage was
unavailable on the previous run. Fresh device codes use the same clipboard
behavior as setup and valid credentials are stored for reuse across repositories.

Status does not modify repository files or secrets and never logs the token.
A successful fresh authorization may create or replace the account-scoped entry
in the operating system's secure credential store.

## Workflow

```yaml
name: CI

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  gh-actions-quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit

  expensive-ci:
    needs: gh-actions-quota
    # Quota threshold (%): change 50 below to adjust this job's limit.
    if: ${{ needs.gh-actions-quota.outputs.usage_available == 'true' && fromJSON(needs.gh-actions-quota.outputs.usage_percent) < 50 }}
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v6
      - run: swift test
```

The generated reusable workflow runs its quota check on `ubuntu-slim` and
defaults to a 50 percent threshold. Setup keeps the caller in the canonical
format above; change the limit directly in each gated job's condition.
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
write repository settings. Expiring tokens and refresh-token responses remain
rejected; the current non-expiring App user token is protected by the OS
credential store instead.

Credential keys are scoped by GitHub host and personal account, so one
authorization is reused for setup and status across repositories on the same
machine. macOS uses Keychain and Windows uses Credential Manager directly. Linux
uses the Secret Service through the standard `secret-tool` command; when it is
not installed or no Secret Service is available, gh-actions-quota deliberately
falls back to in-memory authorization rather than writing a plaintext credential.

If a cached token is rejected as unauthorized/forbidden or belongs to the wrong
account, the CLI discards it and performs the device flow again. Authorization
can also be revoked in your GitHub account's authorized GitHub Apps settings.

See GitHub's [device-flow documentation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
and [billing endpoint permissions](https://docs.github.com/en/rest/billing/usage).

## Quota calculation

The action and CLI read `/users/{account}/settings/billing/usage` for the current
**UTC calendar month** with `product=Actions`. They count only `unitType=minutes`
and these standard GitHub-hosted runner SKUs: `actions_linux_slim`,
`actions_linux`, `actions_linux_arm`, `actions_windows`, `actions_windows_arm`
and `actions_macos`. Included usage is `sum(discountAmount) / 0.006`, expressed
in Linux-equivalent minutes.

Public repositories, larger runners, self-hosted runners, storage and other
products are excluded. Visibility is cached once per repository. A 404 is
conservatively counted; other lookup failures or invalid counted discounts fail
closed. Usage is account-wide across the quota account's private repositories.
Billing data can be delayed or lack repository-level detail; the result depends
on the available report and is **not a real-time spending limit**.

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
for structural validation and targeted YAML edits. Secure login persistence uses
native macOS Keychain and Windows Credential Manager facilities. Linux persistence
uses Secret Service through `secret-tool` when available; it is optional and
there is no insecure file fallback.

After installing the locked development dependencies, validate the action with:

```shell
npm ci
npm run typecheck
npm test
npm run build
git diff --exit-code -- dist/
```

Validate and build the extension:

```shell
go vet ./...
go test -race ./...
go build ./cmd/gh-actions-quota
node --test scripts/release.test.mjs
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

The action and CLI extension share one stable semantic version. `package.json`
is authoritative; the release workflow synchronizes its lockfile automatically.
Current action major: `v1`.

To publish, open **Actions → Release → Run workflow**, select **main**, and choose
**patch**, **minor**, or **major**. Patch is the default. Leave `resume_tag` empty
for a new release. A push to main or a manually pushed tag does not publish a
release. Use a minor increment for compatible new functionality and a major
increment for breaking changes to either the action or extension.

The workflow prepares a local version commit, rebuilds the committed action
bundle, and exports that commit as a Git bundle. Every validation job restores
that exact candidate: TypeScript checks, action tests, release-management tests,
Go tests on Linux/macOS/Windows, and the five native extension builds. Major
increments also update the generated helper's action reference and the current
major documented here.

After validation, the workflow checks that main still matches its starting
commit. If main has advanced, it stops without pushing the candidate; start a
new run against current main. Otherwise it atomically pushes the version commit
and annotated version tag, stages the executable assets in a draft GitHub
Release, verifies their checksums, and publishes generated release notes.

The GitHub Release contains five standalone executables and `checksums.txt`:

- `darwin/arm64`
- `darwin/amd64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`

Asset names have the platform suffix expected by `gh extension install`, for
example `gh-actions-quota_v1.1.0_darwin-arm64` and
`gh-actions-quota_v1.1.0_windows-amd64.exe`. Each executable embeds its version
for `gh actions-quota --version`. There are no archives or runtime dependencies
for users. Only the explicit current-version assets are published; stale local
builds are excluded.

After publication, the corresponding movable major tag (for example `v1` or
`v2`) advances to the newest published stable release of that major. Older
recovery runs cannot move it backwards. Only a release newer than the current
latest stable release is marked latest. Version-specific tags and published
assets are never replaced by this workflow. Major tags must remain ordinary
Git tags without an associated GitHub Release. Repository-level immutable
releases can additionally enforce fixed version tags and assets; the workflow
supports them and assembles every asset before publishing the draft.

### Recover an interrupted release

If a failure happens after the version tag was pushed, run **Release** from
**main** again with `resume_tag` set to that exact tag, for example `v1.1.0`.
The bump choice is ignored. Recovery validates and rebuilds the tagged commit
without changing main, incrementing the version, or moving the version tag.

A missing release is created as a draft; a partial draft is completed and its
assets verified before publication. Incomplete or mismatched assets may be
replaced **only while the release is still a draft**. If publication already
succeeded, recovery verifies the published checksums and assets and can finish
a missing major-tag update. Published assets are never replaced, even when a
new toolchain produces different binaries. Unexpected draft assets or damaged
published assets require manual review; the workflow stops rather than silently
repairing published history. The workflow summary lists the release version,
source commit, all asset names, and major-tag result.

The workflow uses only `GITHUB_TOKEN`, with `contents: write` restricted to the
publishing job. Repository rules must permit that token to update main, create
version tags, and move major tags. No personal access token is needed. Releases
run serially with cancellation disabled; checks run inside the dispatched
workflow rather than relying on token-created pushes to trigger another run.

Installing or upgrading the published extension:

```shell
gh extension install philippwallrafen/gh-actions-quota --force
# For an existing installation:
gh extension upgrade actions-quota
```

Test release management locally with:

```shell
node --test scripts/release.test.mjs
```

Tests use temporary local Git repositories and fake GitHub operations. They
exercise preparation, interruption recovery, and publication policy without
publishing a real release. Implementing or merging changes to this workflow
does not itself publish a release. GitHub App registration settings remain
independent of release management.
