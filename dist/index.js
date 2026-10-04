// src/action.ts
import { readFileSync, appendFileSync } from "node:fs";

// src/constants.ts
var API_VERSION = "2026-03-10";
var LINUX_BASE_PRICE_USD = 6e-3;
var DEFAULT_THRESHOLD_PERCENT = 50;
var INCLUDED_MINUTES_BY_PLAN = Object.freeze({
  free: 2e3,
  pro: 3e3,
  team: 3e3,
  enterprise: 5e4,
  "enterprise cloud": 5e4
});

// src/billing.ts
var SKU_PRICE_PER_MINUTE_USD = {
  actions_linux_slim: 2e-3,
  // 0.3333× Linux
  actions_linux_arm: 5e-3,
  // 0.8333× Linux
  actions_linux: 6e-3,
  // 1.0000× Linux
  actions_windows: 0.01,
  // 1.6667× Linux
  actions_windows_arm: 0.01,
  // 1.6667× Linux
  actions_macos: 0.062
  // 10.3333× Linux
};
function normalizeSku(sku) {
  const normalized = typeof sku === "string" ? sku.trim().toLowerCase().replace(/[\s-]+/g, "_") : "";
  return normalized === "actions_macos_3_core" || normalized === "actions_macos_4_core" ? "actions_macos" : normalized;
}
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("GitHub API response must be an object");
  }
  return value;
}
async function request(url, token, fetchImpl) {
  let response;
  try {
    response = await fetchImpl(url, {
      redirect: "error",
      signal: AbortSignal.timeout(3e4),
      headers: {
        Accept: "application/vnd.github+json",
        Authorization: `Bearer ${token}`,
        "X-GitHub-Api-Version": API_VERSION,
        "User-Agent": "gh-actions-quota"
      }
    });
  } catch {
    throw new Error("GitHub API request failed");
  }
  return response;
}
async function requestJson(url, token, fetchImpl = fetch) {
  const response = await request(url, token, fetchImpl);
  if (!response.ok) throw new Error(`GitHub API returned HTTP ${response.status}`);
  try {
    return object(await response.json());
  } catch {
    throw new Error("GitHub API returned an invalid response");
  }
}
async function isPrivateRepository(repository, token, fetchImpl) {
  const [owner, name] = repository.split("/", 2);
  if (!owner || !name) return false;
  const response = await request(
    `https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
    token,
    fetchImpl
  );
  if (response.status === 404) return false;
  if (!response.ok) throw new Error("GitHub repository lookup failed");
  try {
    return object(await response.json()).private === true;
  } catch {
    throw new Error("GitHub API returned an invalid response");
  }
}
async function parseUsedMinutes(payload, token, fetchImpl = fetch) {
  const report = object(payload);
  if (!Array.isArray(report.usageItems)) {
    throw new Error("GitHub billing response must contain usageItems");
  }
  const privateRepositories = /* @__PURE__ */ new Map();
  let usedMinutes = 0;
  for (const candidate of report.usageItems) {
    const item = object(candidate);
    const skuPrice = SKU_PRICE_PER_MINUTE_USD[normalizeSku(item.sku)];
    if (typeof item.product !== "string" || item.product.toLowerCase() !== "actions" || typeof item.unitType !== "string" || item.unitType.toLowerCase() !== "minutes" || typeof skuPrice !== "number" || typeof item.repositoryName !== "string") continue;
    if (!privateRepositories.has(item.repositoryName)) {
      privateRepositories.set(item.repositoryName, await isPrivateRepository(item.repositoryName, token, fetchImpl));
    }
    if (!privateRepositories.get(item.repositoryName)) continue;
    if (typeof item.quantity !== "number" || !Number.isFinite(item.quantity) || item.quantity < 0) {
      throw new Error("Invalid quantity");
    }
    const factor = skuPrice / LINUX_BASE_PRICE_USD;
    usedMinutes += item.quantity * factor;
  }
  if (!Number.isFinite(usedMinutes)) throw new Error("GitHub billing usage is out of range");
  return usedMinutes;
}
async function fetchOwnerType(owner, token, fetchImpl = fetch) {
  const account = await requestJson(`https://api.github.com/users/${encodeURIComponent(owner)}`, token, fetchImpl);
  const type = String(account.type || "").toLowerCase();
  if (type !== "user" && type !== "organization") throw new Error("Unsupported GitHub owner type");
  return type;
}
async function fetchAuthenticatedUser(token, fetchImpl = fetch) {
  return requestJson("https://api.github.com/user", token, fetchImpl);
}
async function fetchPlan(owner, ownerType, token, fetchImpl = fetch) {
  if (ownerType !== "user") throw new Error("Organization billing is not supported in v1");
  const account = await fetchAuthenticatedUser(token, fetchImpl);
  if (typeof account.login !== "string" || account.login.toLowerCase() !== owner.toLowerCase()) {
    throw new Error("Quota token does not belong to the repository owner");
  }
  const plan = account.plan ? object(account.plan) : void 0;
  if (typeof plan?.name !== "string" || !plan.name) {
    throw new Error("GitHub did not return the account plan; set quota-minutes explicitly");
  }
  return plan.name.toLowerCase();
}
function includedMinutesForPlan(plan) {
  const minutes = INCLUDED_MINUTES_BY_PLAN[plan.trim().toLowerCase()];
  if (!minutes) throw new Error("Unsupported GitHub plan; set quota-minutes explicitly");
  return minutes;
}
async function fetchUsedMinutes(owner, token, { ownerType, now = /* @__PURE__ */ new Date(), fetchImpl = fetch } = {}) {
  const type = ownerType || await fetchOwnerType(owner, token, fetchImpl);
  const endpoint = type === "organization" ? "organizations" : "users";
  const query = new URLSearchParams({
    year: String(now.getUTCFullYear()),
    month: String(now.getUTCMonth() + 1),
    product: "Actions"
  });
  const payload = await requestJson(
    `https://api.github.com/${endpoint}/${encodeURIComponent(owner)}/settings/billing/usage?${query}`,
    token,
    fetchImpl
  );
  return parseUsedMinutes(payload, token, fetchImpl);
}

// src/usage.ts
function parsePositiveNumber(value, label) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    throw new Error(`${label} must be positive and finite`);
  }
  return number;
}
function parseThreshold(value = DEFAULT_THRESHOLD_PERCENT) {
  const threshold = Number(value);
  if (!Number.isFinite(threshold) || threshold <= 0 || threshold > 100) {
    throw new Error("threshold must be greater than 0 and at most 100");
  }
  return threshold;
}
function calculateUsage(usedMinutes, quotaMinutes, threshold = DEFAULT_THRESHOLD_PERCENT) {
  const used = Number(usedMinutes);
  const quota = parsePositiveNumber(quotaMinutes, "quota-minutes");
  const limit = parseThreshold(threshold);
  if (!Number.isFinite(used) || used < 0) {
    throw new Error("used minutes must be non-negative and finite");
  }
  const usagePercent = used / quota * 100;
  return {
    usedMinutes: used,
    quotaMinutes: quota,
    usagePercent,
    remainingMinutes: Math.max(0, quota - used),
    allowed: usagePercent < limit,
    thresholdPercent: limit
  };
}

// src/action.ts
async function runAction(env = process.env, fetchImpl = fetch) {
  const input = (name) => env[`INPUT_${name.toUpperCase()}`] || env[`INPUT_${name.toUpperCase().replaceAll("-", "_")}`] || "";
  const summary = (markdown) => {
    if (env.GITHUB_STEP_SUMMARY) appendFileSync(env.GITHUB_STEP_SUMMARY, markdown, "utf8");
  };
  const writeResult = (result) => {
    for (const [name, value] of Object.entries(result)) {
      const line = `${name}=${String(value).replace(/[\r\n]/g, " ")}
`;
      if (env.GITHUB_OUTPUT) appendFileSync(env.GITHUB_OUTPUT, line, "utf8");
      else console.log(line.trimEnd());
    }
  };
  try {
    const threshold = parseThreshold(input("threshold") || DEFAULT_THRESHOLD_PERCENT);
    if (publicRepositoryContext(env)) {
      writeResult({
        "usage-available": true,
        "used-minutes": 0,
        "quota-minutes": "unmetered",
        "usage-percent": 0,
        "remaining-minutes": "unmetered",
        allowed: true,
        "billing-owner": env.GITHUB_REPOSITORY_OWNER || "unknown",
        "billing-owner-type": "unmetered",
        unmetered: true
      });
      summary("## GitHub Actions quota\n\nStandard GitHub-hosted runners are unmetered for this public repository.\n");
      return;
    }
    const owner = env.GITHUB_REPOSITORY_OWNER;
    const token = input("token") || env.ACTIONS_QUOTA_TOKEN;
    if (!owner) throw new Error("GITHUB_REPOSITORY_OWNER is unavailable");
    if (!token) throw new Error("Missing ACTIONS_QUOTA_TOKEN");
    const ownerType = await fetchOwnerType(owner, token, fetchImpl);
    if (ownerType !== "user") throw new Error("Organization billing is not supported in v1");
    const explicitQuota = input("quota-minutes");
    const quotaMinutes = explicitQuota ? parsePositiveNumber(explicitQuota, "quota-minutes") : includedMinutesForPlan(await fetchPlan(owner, ownerType, token, fetchImpl));
    const usedMinutes = await fetchUsedMinutes(owner, token, { ownerType, fetchImpl });
    const usage = calculateUsage(usedMinutes, quotaMinutes, threshold);
    writeResult({
      "usage-available": true,
      "used-minutes": formatNumber(usage.usedMinutes),
      "quota-minutes": formatNumber(usage.quotaMinutes),
      "usage-percent": formatNumber(usage.usagePercent),
      "remaining-minutes": formatNumber(usage.remainingMinutes),
      allowed: usage.allowed,
      "billing-owner": owner,
      "billing-owner-type": ownerType,
      unmetered: false
    });
    summary(`## GitHub Actions quota

Usage: **${formatNumber(usage.usedMinutes)} / ${formatNumber(usage.quotaMinutes)} minutes (${formatNumber(usage.usagePercent)}%)**

Threshold: **${formatNumber(threshold)}%**

Allowed: **${usage.allowed}**
`);
  } catch {
    const message = "Billing usage unavailable. Check the token, account plan, billing access and action inputs.";
    console.log(`::warning title=Actions quota::${message}`);
    writeResult({
      "usage-available": false,
      "used-minutes": "unavailable",
      "quota-minutes": input("quota-minutes") || "unavailable",
      "usage-percent": "unavailable",
      "remaining-minutes": "unavailable",
      allowed: false,
      "billing-owner": env.GITHUB_REPOSITORY_OWNER || "unknown",
      "billing-owner-type": "unavailable",
      unmetered: false
    });
    summary(`## GitHub Actions quota

Usage unavailable.

${message}
`);
  }
}
function publicRepositoryContext(env) {
  if (env.GITHUB_REPOSITORY_VISIBILITY === "public") return true;
  if (!env.GITHUB_EVENT_PATH) return false;
  try {
    const event = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8"));
    return event?.repository?.private === false;
  } catch {
    return false;
  }
}
function formatNumber(value) {
  return Number(value.toFixed(6)).toString();
}

// src/index.ts
void runAction();
