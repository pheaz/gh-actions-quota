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
