import {
  API_VERSION,
  INCLUDED_MINUTES_BY_PLAN,
  LINUX_MINUTE_PRICE_USD,
} from "./constants.js";

function encode(value) {
  return encodeURIComponent(value);
}

export async function requestJson(url, token, fetchImpl = fetch) {
  const response = await fetchImpl(url, {
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": API_VERSION,
      "User-Agent": "github-actions-quota",
    },
  });

  if (!response.ok) {
    const detail = (await response.text()).replace(/\s+/g, " ").slice(0, 300);
    throw new Error(`GitHub API ${response.status}: ${detail || response.statusText}`);
  }
  return response.json();
}

export function parseUsedMinutes(payload) {
  if (!payload || !Array.isArray(payload.usageItems)) {
    throw new Error("GitHub billing response must contain usageItems");
  }

  let discountAmount = 0;
  for (const item of payload.usageItems) {
    if (
      item &&
      item.product === "Actions" &&
      item.unitType === "minutes" &&
      Number.isFinite(item.discountAmount)
    ) {
      discountAmount += item.discountAmount;
    }
  }

  return discountAmount / LINUX_MINUTE_PRICE_USD;
}

export async function fetchOwnerType(owner, token, fetchImpl = fetch) {
  const account = await requestJson(
    `https://api.github.com/users/${encode(owner)}`,
    token,
    fetchImpl,
  );
  const type = String(account.type || "").toLowerCase();
  if (type !== "user" && type !== "organization") {
    throw new Error(`Unsupported GitHub owner type: ${account.type || "unknown"}`);
  }
  return type;
}

export async function fetchAuthenticatedUser(token, fetchImpl = fetch) {
  return requestJson("https://api.github.com/user", token, fetchImpl);
}

export async function fetchPlan(owner, ownerType, token, fetchImpl = fetch) {
  const account =
    ownerType === "user"
      ? await fetchAuthenticatedUser(token, fetchImpl)
      : await requestJson(
          `https://api.github.com/orgs/${encode(owner)}`,
          token,
          fetchImpl,
        );

  if (String(account.login || "").toLowerCase() !== owner.toLowerCase()) {
    throw new Error(
      `Quota token belongs to ${account.login || "another account"}, not ${owner}`,
    );
  }

  const name = account.plan?.name;
  if (!name) {
    throw new Error(
      "GitHub did not return the account plan; set quota-minutes explicitly",
    );
  }
  return String(name).toLowerCase();
}

export function includedMinutesForPlan(plan) {
  const normalized = String(plan).trim().toLowerCase();
  const minutes = INCLUDED_MINUTES_BY_PLAN[normalized];
  if (!minutes) {
    throw new Error(
      `Unsupported GitHub plan ${JSON.stringify(plan)}; set quota-minutes explicitly`,
    );
  }
  return minutes;
}

export async function fetchUsedMinutes(
  owner,
  token,
  { ownerType, now = new Date(), fetchImpl = fetch } = {},
) {
  const type = ownerType || (await fetchOwnerType(owner, token, fetchImpl));
  const resource = type === "user" ? "users" : "organizations";
  const query = new URLSearchParams({
    year: String(now.getUTCFullYear()),
    month: String(now.getUTCMonth() + 1),
    product: "Actions",
  });
  const payload = await requestJson(
    `https://api.github.com/${resource}/${encode(owner)}/settings/billing/usage/summary?${query}`,
    token,
    fetchImpl,
  );
  return parseUsedMinutes(payload);
}
