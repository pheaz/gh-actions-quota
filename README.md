# github-actions-quota

`github-actions-quota` is a small, standard-library-only Python package that
measures GitHub Actions quota usage. It converts Actions billing discounts to
Linux-equivalent included minutes at `$0.006/min`. Repositories—not this
package—define their CI thresholds directly in workflow job conditions.

## Installation

Add the package to a dedicated CI dependency group with uv:

```shell
uv add --group ci github-actions-quota==0.1.0
```

Alternatively, install it with pip:

```shell
python -m pip install github-actions-quota==0.1.0
```

When using the uv group, run the installed console script from the locked
environment with `uv run --locked --group ci github-actions-quota`.

## Python API

```python
from decimal import Decimal

from github_actions_quota import calculate_usage

usage = calculate_usage(Decimal("1200"))
print(usage.usage_percent)  # 60.0
```

The public API also provides `fetch_used_minutes` and `parse_used_minutes` for
retrieving and parsing billing usage. Personal repository owners use GitHub's
user billing endpoint; organization owners use its organization billing
endpoint.

## Authentication and configuration

Configure trusted GitHub Actions runs with:

```text
Repository/organization secret:
ACTIONS_QUOTA_TOKEN

Repository/organization variable:
ACTIONS_QUOTA_MINUTES=2000
```

The token needs Plan read permission for a user account or organization
Administration read permission for an organization. The quota variable is
optional and defaults to 2,000 minutes. GitHub Actions automatically supplies
`GITHUB_REPOSITORY_OWNER`; do not configure it yourself.

Locally, `ACTIONS_QUOTA_TOKEN` takes precedence. When it is absent, the command
uses `gh auth token`, so developers can run `github-actions-quota` after
`gh auth login`. It resolves the current repository owner with `gh` when
`GITHUB_REPOSITORY_OWNER` is also absent.

Billing always belongs to the repository owner, never `GITHUB_ACTOR` or a pull
request author. All contributors and coding agents working in a repository
therefore share its owner's quota; their personal GitHub quotas are irrelevant.

For a public repository in the normal Actions workflow context, the command
reports zero percent without requiring a billing token. Included-minute quota
is not applicable because standard GitHub-hosted runners are free for public
repositories. This does not describe larger runners or other separately billed
runner products.

## CI integration

The CLI writes `usage_available`, `used_minutes`, `quota_minutes`,
`usage_percent`, `billing_owner`, and `billing_owner_type` to `GITHUB_OUTPUT`.
Keep each quality check as its own job and put readable thresholds directly in
the consuming workflow:

```yaml
jobs:
  quota:
    runs-on: ubuntu-latest
    outputs:
      usage_available: ${{ steps.quota.outputs.usage_available }}
      usage_percent: ${{ steps.quota.outputs.usage_percent }}
    steps:
      - uses: actions/checkout@v4
      - id: quota
        env:
          ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
          ACTIONS_QUOTA_MINUTES: ${{ vars.ACTIONS_QUOTA_MINUTES || '2000' }}
        run: uv run --locked --group ci github-actions-quota

  tests:
    needs: quota
    if: needs.quota.outputs.usage_percent < 50
    # Test steps.

  lock:
    needs: quota
    if: needs.quota.outputs.usage_percent < 100
    # Lockfile check.
```

For fail-closed gating, also include
`needs.quota.outputs.usage_available == 'true'` as this repository's workflow
does. Authentication or API failures emit a warning and an unavailable result,
but exit successfully so dependent jobs are intentionally skipped rather than
reported as failures.

## Development

Install the development tools from the lockfile with
`uv sync --locked --group dev`. From this repository checkout, run the CLI with
`uv run github-actions-quota`.
