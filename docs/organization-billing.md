# Organization billing API contract

Verified against current official GitHub documentation before implementation,
2026-10-05. These are github.com REST endpoints, with
`Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2026-03-10`.

| Purpose | Endpoint | GitHub App permission |
| --- | --- | --- |
| Owner type | `GET /users/{owner}` (`type=User/Organization`) | Public resource read |
| Organization plan | `GET /orgs/{org}` (`login`, `type`, `plan.name`) | Organization plan: read for the plan field |
| Organization usage | `GET /organizations/{org}/settings/billing/usage` | Organization Administration: read |
| Repository visibility | `GET /repos/{owner}/{repo}` (`private`) | Repository Metadata: read for private repositories |
| Existing personal plan | `GET /user` | Account Plan: read |
| Existing personal usage | `GET /users/{owner}/settings/billing/usage` | Account Plan: read |

The [organization usage reference](https://docs.github.com/en/rest/billing/usage?apiVersion=2026-03-10#get-billing-usage-report-for-an-organization)
explicitly supports GitHub App user access tokens, installation access tokens,
and fine-grained PATs. gh-actions-quota preserves device flow with a non-expiring
App user token; it does not require installation-token private keys, client
secrets, a server, refresh tokens, or classic PATs. GitHub's generic
[reporting tutorial](https://docs.github.com/en/billing/tutorials/automate-usage-reporting)
currently says classic PATs are required, which conflicts with the endpoint's
specific token documentation; this implementation follows the endpoint reference.
The endpoint requires an organization administrator and access to the enhanced
billing platform. The tutorial mentions owners/billing managers, but no user role
is presumed sufficient without a successful API request.

The report has a `usageItems` array. Each row includes `date`, `product`, `sku`,
`quantity`, `unitType`, `pricePerUnit`, `grossAmount`, `discountAmount`, `netAmount`,
`organizationName`, and `repositoryName` (in `owner/repository` form). Calculation
uses `quantity` and the established SKU allowlist/rates, never monetary fields.
The org report documents year/month/day filters, not product/repository or
page/per_page. We request the current UTC year/month and filter products locally.
It documents no pagination or next-page mechanism. A `Link: ...; rel="next"`
response is rejected to avoid treating an undocumented partial report as total
usage. Responses exceeding the existing 4 MiB safety limit also fail closed.
The preview `/usage/summary` endpoint has gross/net quantity fields and lacks
repository detail needed to exclude public usage, so it is not substituted.

The [organization reference](https://docs.github.com/en/rest/orgs/orgs?apiVersion=2026-03-10#get-an-organization)
requires Organization plan permission for `plan`, even though public organization
metadata needs no permission. The example plan name `Medium` is a legacy name,
not evidence of a current quota. The usage report itself has no included quota.
Only explicit Free/Team plan names map to the
[published organization allowances](https://docs.github.com/en/billing/reference/product-usage-included)
(2,000/3,000). Enterprise/legacy/custom/absent plan data is not inferred. In
particular, an Enterprise name alone cannot establish organization-specific
allowance or trial status; GitHub documents that
[enterprise trials do not get the paid plan's 50,000 minutes](https://docs.github.com/en/enterprise-cloud@latest/admin/overview/setting-up-a-trial-of-github-enterprise-cloud).
Verify the applicable allowance and billing scope in billing settings and use
`quota-minutes`. The report remains organization-scoped; an override does not
turn it into enterprise-wide usage reporting or a real-time spending limit.

The [repository endpoint](https://docs.github.com/en/rest/repos/repos?apiVersion=2026-03-10#get-a-repository)
requires Metadata read for private visibility. Organization calculations require
visibility for every counted repository; install with sufficient repository
coverage (usually all repositories). Missing names, 404s, or malformed visibility
fail closed. Personal report exclusions remain compatible with the existing
contract. Historical visibility changes cannot be reconstructed by this API.

Organization resources need an App installation on that organization. User-token
access is also limited by the authorizing user's privileges; successful device
login alone cannot grant organization administration. An organization owner must
approve installation and permission updates. See
[choosing App permissions](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app)
and [permission approval](https://docs.github.com/en/apps/maintaining-github-apps/modifying-a-github-app-registration#changing-the-permissions-of-a-github-app).
403/404 access failures explain installation, approval, billing privileges and
organization/SSO policy without claiming which one GitHub has hidden. They
preserve cached user authorization. A quota override skips plan detection but
cannot bypass usage/visibility access checks. No production App settings are
changed by the repository or CLI.
