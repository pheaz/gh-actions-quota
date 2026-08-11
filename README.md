# github-actions-quota

`github-actions-quota` is a small, standard-library-only Python package for
quota-aware GitHub Actions CI gating. It converts Actions billing discounts to
Linux-equivalent included minutes at `$0.006/min`, then selects full, minimal,
or no CI. Consuming repositories decide which checks belong to minimal CI.

## Python API

```python
from decimal import Decimal

from github_actions_quota import evaluate_quota

decision = evaluate_quota(Decimal("1200"))
print(decision.state)
```

The public API also provides `fetch_used_minutes` and `parse_used_minutes` for
retrieving or parsing billing usage.

## GitHub Actions CLI

Set `GITHUB_ACTIONS_QUOTA_TOKEN` to a GitHub token capable of reading the user's
billing/plan usage, and set `GITHUB_REPOSITORY_OWNER`. Do not store the token in
source; pass it through GitHub Actions secrets. Then run:

```console
github-actions-quota
```

The optional `GITHUB_ACTIONS_QUOTA_MINUTES` overrides the default 2,000-minute
quota. In Actions, the command writes policy fields to `GITHUB_OUTPUT` and a
human-readable report to `GITHUB_STEP_SUMMARY`.

## CI integration

Use the package directly from `src` in a controller job, then gate expensive
and minimal jobs with its outputs. Fork pull requests take a separate path so
they receive no billing secret while still running the full suite:

```yaml
jobs:
  quota:
    runs-on: ubuntu-latest
    outputs:
      ci_state: ${{ steps.quota.outputs.ci_state || steps.fork.outputs.ci_state }}
      ci_reason: ${{ steps.quota.outputs.ci_reason || steps.fork.outputs.ci_reason }}
      full_ci: ${{ steps.quota.outputs.full_ci || steps.fork.outputs.full_ci }}
      minimal_ci: ${{ steps.quota.outputs.minimal_ci || steps.fork.outputs.minimal_ci }}
    steps:
      - uses: actions/checkout@v4
      - id: quota
        if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.fork == false
        env:
          PYTHONPATH: src
          GITHUB_ACTIONS_QUOTA_TOKEN: ${{ secrets.GITHUB_ACTIONS_QUOTA_TOKEN }}
          GITHUB_REPOSITORY_OWNER: ${{ github.repository_owner }}
        run: python3 -m github_actions_quota
      - id: fork
        if: github.event_name == 'pull_request' && github.event.pull_request.head.repo.fork == true
        run: |
          echo "ci_state=full" >> "$GITHUB_OUTPUT"
          echo "ci_reason=fork_pull_request" >> "$GITHUB_OUTPUT"
          echo "full_ci=true" >> "$GITHUB_OUTPUT"
          echo "minimal_ci=true" >> "$GITHUB_OUTPUT"

  quality:
    needs: quota
    if: needs.quota.outputs.full_ci == 'true'
    # Full test and lint steps.

  lock:
    needs: quota
    if: needs.quota.outputs.minimal_ci == 'true'
    # Minimal lockfile check.
```
