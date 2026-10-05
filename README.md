# gh-actions-quota

Monitor your included GitHub Actions quota and prevent expensive CI jobs from
running after a configurable usage threshold is reached.

`gh-actions-quota` combines:

- a **GitHub Action** for gating workflow jobs based on Actions quota usage;
- a **GitHub CLI extension** for setup, authentication and quota status.

It supports personal accounts and organizations on github.com using standard
GitHub-hosted runners. Public repositories are detected as unmetered.

## Quick start

Install the GitHub CLI extension:

```shell
gh extension install philippwallrafen/gh-actions-quota
```

Authenticate the quota account once, then check the current quota:

```shell
gh actions-quota auth login
gh actions-quota status
```

Example:

```text
Repository: philippwallrafen/example
Visibility: Private (metered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: philippwallrafen
  Owner type: user
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

Configure a private repository:

```shell
gh actions-quota setup
```

The setup command creates the reusable quota workflow and stores the required
token as the repository secret `ACTIONS_QUOTA_TOKEN`. For private repositories,
setup can also start the GitHub App device flow automatically when no reusable
authorization exists.

> [!NOTE]
> Public repositories using standard GitHub-hosted runners do not consume the
> included Actions quota and therefore require no repository setup.

## Setup

Install [GitHub CLI](https://cli.github.com/) and authenticate with
`gh auth login`. Then run these commands from the repository that will use the action:

```shell
gh extension install philippwallrafen/gh-actions-quota --force
gh actions-quota setup
```

For a **private repository**, setup first looks for an existing gh-actions-quota
authorization for the billing owner in the operating system's secure
credential store. If none is available, setup opens GitHub's device authorization
page and copies the device code to the clipboard before opening the browser when
clipboard support is available. For personal ownership, authorize as the
**personal account that owns the current repository**. After validating the
account identity, plan and billing access, the extension stores the App token in the OS credential store and saves
the same token as the repository secret **`ACTIONS_QUOTA_TOKEN`** using your
existing local `gh` login. The cached authorization is account-scoped, not
repository-scoped, so other private repositories owned by the same personal
account can reuse it without another device login. For an organization owner,
authorize as a personal user with organization billing privileges; the credential
is scoped to the organization, and usage covers its private repositories. See
[organization authorization](#organization-authorization) before running setup.

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
and refuses to overwrite custom helper files. The previous generated personal
helper remains supported without rewriting. An explicit `--quota-minutes` updates
only a recognized generated helper and preserves its file permissions.

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
arguments or prints it. Setup and `auth login` persist authorization only in
the operating system's secure credential store: macOS Keychain, Windows
Credential Manager, or the Linux Secret Service via `secret-tool`. Explicit
`auth login` requires secure storage so a successful login is actually reusable.
Private-repository setup can still continue with an in-memory authorization when
secure storage is unavailable and will request authorization again next time.
`status` never starts device flow and requires a stored authorization for metered
quota reporting; public repositories can be reported as unmetered without one.
Repository secret writes continue to pipe the token to `gh secret set` through
stdin. There is no server, central token store or telemetry.

Public repositories using standard GitHub-hosted runners need no workflow setup
or repository token. Status can still show a personal account's private quota
from a public repository, including one owned by an organization.

## Authentication

GitHub CLI authentication and gh-actions-quota App authentication are separate:

```shell
gh auth login
gh actions-quota auth login
```

Manage the account-scoped gh-actions-quota authorization explicitly with:

```shell
gh actions-quota auth login
gh actions-quota auth status
gh actions-quota auth logout
```

Inside a private repository, these commands target the billing owner, either a
personal account or an organization. Inside a public repository, or outside any
repository, they target the personal account currently authenticated with `gh` on github.com.

`auth login` reuses a valid stored authorization or starts GitHub's device flow
and stores the resulting App token in the operating system's secure credential
store. `auth status` validates the stored credential without opening a browser.
`auth logout` removes only that local credential. It does not revoke the GitHub
App authorization on GitHub and does not change repository workflows or secrets.

Private-repository `setup` remains the intentional exception: setup may start
device flow automatically when authorization is required. Other read-only
commands such as `status` do not.

## Uninstall

Remove repository-specific gh-actions-quota setup with:

```shell
gh actions-quota uninstall
```

Uninstall removes recognized quota callers and job gates from local workflow
files, removes the generated `.github/workflows/gh-actions-quota.yml` helper,
and deletes the repository secret `ACTIONS_QUOTA_TOKEN` when present. The
account-scoped App authorization is kept; use `gh actions-quota auth logout`
when you also want to remove the local credential.

The command only removes structures it can identify safely. A modified generated
helper or a custom/ambiguous quota expression must be reconciled manually before
uninstall continues.

## Shell completion

The CLI uses Cobra and can generate completion scripts for the supported shells:

```shell
gh actions-quota completion bash
gh actions-quota completion zsh
gh actions-quota completion fish
gh actions-quota completion powershell
```

Load or install the generated script using the normal completion mechanism for
your shell.

## Status

Run `gh actions-quota status` from a repository checkout or from any other
directory. When no repository can be resolved, status still shows the current
personal GitHub account's private Actions quota:

```text
Repository: not found

Actions quota:
  Account: philippwallrafen
  Owner type: user
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

When a repository is available, status also reports whether that repository is
metered and inspects its gh-actions-quota setup.

For a **public repository**, status retains the personal quota display when a
usable authorization exists for the personal account currently signed in with
`gh` on github.com. Without that authorization it reports the repository as
unmetered successfully, with no device flow or organization billing requests.
Public repositories are unmetered on standard GitHub-hosted runners. Setup artifacts are shown only when they are present:

```text
Repository: some-org/example
Visibility: Public (unmetered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: philippwallrafen
  Owner type: user
  Used:    742.33 / 2000 min  ( 37.12% )
  Plan:    Free
```

If neither `.github/workflows/gh-actions-quota.yml` nor
`ACTIONS_QUOTA_TOKEN` is present, the `Setup` block is omitted entirely for
public repositories. A present workflow or secret is shown individually; missing
public setup artifacts are never printed.

For a **private repository**, status uses the repository owner's personal or
organization quota. Both setup states are always shown:

```text
Repository: philippwallrafen/example
Visibility: Private (metered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: philippwallrafen
  Owner type: user
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
displayed billing owner's private Actions usage across its repositories:
the current `gh` account for public repositories or when no repository is found,
and the repository owner for private repositories.

Metered quota status requires a usable authorization already stored for the
relevant billing owner. Status never starts device flow or writes a new
credential. If the credential is missing, invalid, revoked, or belongs to the
wrong account, status exits with guidance to run:

```shell
gh actions-quota auth login
```

Status does not modify repository files or secrets and never logs the token.

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
| Repository permissions | **Metadata: Read-only**, for visibility of repositories in an organization billing report; no Contents, Actions, Secrets, or write access |
| Organization permissions | **Administration: Read-only** for usage; **Organization plan: Read-only** for automatic allowance detection |
| Device Flow | Enabled |
| User-to-server token expiration | Disabled |
| Client secret | Not used |
| Homepage | https://github.com/philippwallrafen/gh-actions-quota |

The app token reads plan, billing and repository visibility data. GitHub requires
organization Administration read access for the usage endpoint; this permission
also permits other organization administration reads. The app only calls the
documented read endpoints described below. The app does
not request `Secrets: write`. Only the locally authenticated `gh` process writes
the repository secret. The action subsequently reads that secret; it does not
write repository settings. Expiring tokens and refresh-token responses remain
rejected; the current non-expiring App user token is protected by the OS
credential store instead.

Credential keys are scoped by GitHub host and billing owner (user or organization), so one
authorization is reused for setup and status across repositories on the same
machine. macOS uses Keychain and Windows uses Credential Manager directly. Linux
uses the Secret Service through the standard `secret-tool` command; when it is
not installed or no Secret Service is available, gh-actions-quota deliberately
falls back to in-memory authorization rather than writing a plaintext credential.

For personal accounts, a cached token rejected as unauthorized/forbidden or
belonging to the wrong account is discarded as before. Organization billing
permission/installation failures preserve the credential and explain the required
owner approval or user privileges; repeating device login alone cannot grant
organization access. Unauthorized tokens can still be replaced through auth login. Authorization
can also be revoked in your GitHub account's authorized GitHub Apps settings.

See GitHub's [device-flow documentation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
and [billing endpoint permissions](https://docs.github.com/en/rest/billing/usage).

## Organization authorization

Organization support needs a manual GitHub App registration update by the App
maintainer: keep Account Plan read, Device Flow enabled and non-expiring user
tokens, and add the organization permissions in the table above. This code does
not change production App settings. The organization usage endpoint's minimum
organization permission is Administration read; Organization plan read is needed
only for automatic quota detection. Repository Metadata read is necessary to
exclude public usage safely; no repository Contents/Actions/Secrets access is
needed by the App. The local `gh` login still needs repository secret write access
for setup, independently of the billing token.

An organization owner must install the App on the organization and approve any
new permissions on an existing installation. Device authorization alone does not
install the App or approve its organization permissions. Authorize as an
organization owner/admin with billing access. GitHub's reporting tutorial also
mentions billing managers, but the REST endpoint specifically requires an
organization administrator; a billing-manager title alone is not assumed to
satisfy the App endpoint. The user token is limited by both the App installation
and the user's privileges. Organization policies or SSO may require additional
approval. See GitHub's [installation guide](https://docs.github.com/en/apps/using-github-apps/installing-a-github-app-from-a-third-party),
[permission changes](https://docs.github.com/en/apps/maintaining-github-apps/modifying-a-github-app-registration#changing-the-permissions-of-a-github-app)
and [usage endpoint](https://docs.github.com/en/rest/billing/usage#get-billing-usage-report-for-an-organization).

Grant the App Metadata read access to every repository with counted usage in the
organization report, usually by installing on all repositories. A missing or
unreadable repository, including a 404 that could hide a private repository,
makes organization usage unavailable. A quota override cannot fix this.
Public organization repositories require no App setup or organization approval
just to be reported as unmetered.

Run inside a private organization checkout:

```shell
cd pheaz/example
gh actions-quota setup
gh actions-quota status
```

Example status after owner approval and device authorization:

```text
Repository: pheaz/example
Visibility: Private (metered)

Setup:
  Workflow: present
  Secret:   present

Actions quota:
  Account: pheaz
  Owner type: organization
  Used:    750.00 / 3000 min  ( 25.00% )
  Plan:    Team
```

If GitHub cannot expose a reliable included allowance, verify it in billing
settings and provide it explicitly (4,000 below is an example, not an inferred plan):

```shell
gh actions-quota setup --quota-minutes 4000
gh actions-quota status --quota-minutes 4000
```

Setup records the value as the generated helper's `quota-minutes` input default.
Subsequent setup/status calls reuse that default. The status flag overrides it
for that invocation without editing files. New helpers forward the optional
string input to the Action; canonical workflow callers stay unchanged. Modified
helpers still require manual reconciliation. Re-run setup with an updated value
when the verified allowance changes.

Existing personal credentials, secrets, and generated helpers keep working.
Organization users must install/approve the updated App, authorize with
`gh actions-quota auth login` or setup, and run setup in each repository to store
the billing token. Installation permission approval can restore access for an
existing valid user token without discarding it. Outside a repository, auth and
status continue to use the current personal account.

## Quota calculation

The Action and CLI use the same Go implementation to estimate the current UTC
month's private-repository standard-runner usage in Linux-equivalent minutes.
Public repositories, self-hosted runners, storage and larger runners are excluded. Billing data can
be delayed, so this is not a real-time spending limit.

The formula, SKU prices, normalization and filtering rules are defined in the
[normative quota calculation specification](spec/quota-calculation.md).

| Plan | Included monthly minutes |
| --- | ---: |
| Free | 2,000 |
| Pro | 3,000 |
| Team | 3,000 |
| Enterprise Cloud | 50,000 |

Personal plan mappings remain unchanged. Organization Free (2,000) and Team
(3,000) are detected from `GET /orgs/{org}` with Organization plan read access,
using GitHub's [published allowances](https://docs.github.com/en/billing/reference/product-usage-included).
The organization usage report contains no included quota field. Missing/unknown
plans, legacy names such as `Medium`, and enterprise plans require a verified
explicit allowance; personal Pro and legacy aliases are never assumed to be
organization Team. Enterprise plan names alone do not establish the allowance
for an organization, including trials or enterprise billing arrangements.

When organization usage is obtained but quota detection fails, the Action reports
numeric `used-minutes` and `billing-owner-type=organization`, with
`quota-minutes`, `remaining-minutes` and `usage-percent` set to `unavailable`.
It warns clearly and sets `usage-available=false` and `allowed=false` so existing
workflow gates safely skip jobs. Setup/status similarly show the obtained usage
and return an actionable error instead of inventing an allowance.

An explicit positive, finite `quota-minutes` skips organization plan detection,
while still requiring valid organization usage and confirmed repository visibility.
It does not grant authorization or bypass permission failures. For an unusual
personal plan or an organization requiring a fallback, set it on the Action:

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
| `usage-available` | Whether usage and allowance are usable for quota gating (false when organization quota is unavailable, even if usage was obtained) |
| `used-minutes` | Linux-equivalent included minutes consumed |
| `quota-minutes` | Detected or overridden monthly allowance |
| `remaining-minutes` | Remaining minutes, with a minimum of zero |
| `usage-percent` | Percent of included allowance consumed |
| `billing-owner` | Repository owner whose quota is charged |
| `billing-owner-type` | `user` or `organization`; `unmetered` for public repos; `unavailable` on lookup/billing failure |
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

All quota and Action behavior lives in Go. `action.yml` uses Node.js 24 to run
`action/launcher.js`, a dependency-free bootstrap that downloads the exact
`package.json` release version, verifies SHA256 against that release's
`checksums.txt`, and executes `gh-actions-quota action`. Release assets must be
reachable from the runner; download and checksum failures fail the step.
See [ADR 0001](docs/adr/0001-go-core-js-action-launcher.md) for the architecture.

The extension uses Go 1.27.1 or newer. It uses `golang.org/x/term` for the
cross-platform interactive workflow and job checklists, and `go.yaml.in/yaml/v3`
for structural validation and targeted YAML edits. Secure login persistence uses
native macOS Keychain and Windows Credential Manager facilities. Linux persistence
uses Secret Service through `secret-tool` when available; it is optional and
there is no insecure file fallback.

Validate the launcher and release tooling with Node.js (no npm installation or
build is required):

```shell
node --check action/launcher.js
node --test action/launcher.test.js
node --test scripts/release.test.mjs
```

Validate and build the shared Go core, Action command and extension:

```shell
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test -race ./...
go build ./cmd/gh-actions-quota
bash scripts/build-release.sh "v$(node -p 'require("./package.json").version')"
node scripts/release.mjs verify-assets "v$(node -p 'require("./package.json").version')" release
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

The workflow prepares a local version commit and exports it as a Git bundle.
Every validation job restores that exact candidate: launcher tests,
release-management tests, Go quota and Action tests on Linux/macOS/Windows, and
all five native release builds with checksum verification. Candidate checks test
these layers independently, without downloading unpublished candidate assets.
Major increments also update the generated helper's action reference and the
current major documented here.

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
