# Quota calculation contract

This is the normative quota calculation for the shared Go implementation in
`internal/quota`, used by setup, status and the GitHub Action. Architecture is
defined in [ADR 0001](../docs/adr/0001-go-core-js-action-launcher.md).

## Billing period and source

Read the detailed `/users/{owner}/settings/billing/usage` endpoint with
`year` and `month` selected from the current **UTC calendar month**, and
`product=Actions`. Do not use `/usage/summary`. The shared billing reader can
select `/organizations/{owner}/settings/billing/usage`, but organization quota
gating remains unsupported in v1.

## Prices and normalization

The normalization baseline is standard Linux 2-core at **0.006 USD/minute**.
Prices are the following contract values; factors are derived by dividing each
price by 0.006 and must not be stored as independent constants.

| Internal SKU | USD/minute | Derived Linux factor |
| --- | ---: | ---: |
| `actions_linux_slim` | 0.002 | 0.333333… |
| `actions_linux_arm` | 0.005 | 0.833333… |
| `actions_linux` | 0.006 | 1 |
| `actions_windows` | 0.010 | 1.666666… |
| `actions_windows_arm` | 0.010 | 1.666666… |
| `actions_macos` | 0.062 | 10.333333… |

Trim and lowercase SKU names, replacing whitespace and hyphens with underscores.
Thus real API names such as `Actions Linux Slim`, `Actions Linux ARM`,
`Actions Windows ARM` and `Actions Linux` map to the keys above. Standard
`Actions macOS 3-core` and `Actions macOS 4-core` map to `actions_macos`.
Match specific Slim/ARM variants before generic names. Only exact standard
names are accepted; unknown core-count suffixes must not match a generic SKU.

## Filtering and formula

For each usage item:

1. Trim and compare product `actions` and unit `Minutes` case-insensitively.
2. Accept only the standard runner SKUs in the table.
3. Require a repository name and confirm that repository is currently private
   through the repository API. Cache visibility once per repository per report.
   Public repositories, missing/malformed names, unavailable (404) repositories
   and repositories without confirmed private visibility are excluded.
4. Require the counted item's `quantity` to be a finite, non-negative number.

Ignore storage, other products/units, unknown SKUs, self-hosted and larger
runners before visibility lookup. Repository lookup errors other than 404,
malformed billing responses, invalid counted quantities and numerical overflow
make usage unavailable and fail closed. API errors must be sanitized.

```text
effectiveMinutes(item) = quantity(item) × (skuPricePerMinuteUSD(item) / 0.006)
usedMinutes = sum(effectiveMinutes(item))
usagePercent = (usedMinutes / quotaMinutes) × 100
remainingMinutes = max(0, quotaMinutes - usedMinutes)
allowed = usagePercent < thresholdPercent
```

Use `quantity`, never `discountAmount`, `grossAmount`, `netAmount` or the report's
`pricePerUnit`, to calculate quota. Round only for presentation; the threshold
comparison uses the unrounded result. Exactly at the threshold is disallowed.
Quota must be positive and finite; threshold must be greater than zero and at
most 100 percent (default 50).

Personal plan allowances are Free: 2,000, Pro: 3,000, Team: 3,000 and
Enterprise/Enterprise Cloud: 50,000 minutes. Unknown plans fail closed unless
the Action has an explicit valid `quota-minutes` override. Such an override
skips plan detection. Team/Enterprise mappings do not enable organization gating.
Public Action contexts short-circuit billing as unmetered, with zero usage and
`allowed=true`, after threshold validation.

## Known limitation

Repository visibility is checked at calculation time. Historical public/private
transitions are not reconstructed, so current visibility can exclude former
private usage or include former public usage. Billing reports may also be
delayed or lack repository detail; this estimate is not a real-time spending
limit.
