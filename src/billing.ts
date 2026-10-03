import { API_VERSION, INCLUDED_MINUTES_BY_PLAN, LINUX_MINUTE_PRICE_USD } from "./constants.js";

export type Fetch = typeof fetch;
export type OwnerType = "user" | "organization";

const INCLUDED_SKUS = new Set([
  "actions_linux_slim",
  "actions_linux",
  "actions_linux_arm",
  "actions_windows",
  "actions_windows_arm",
  "actions_macos",
]);

function normalizeSku(sku: unknown): string {
  return typeof sku === "string" ? sku.trim().toLowerCase().replace(/[\s-]+/g, "_") : "";
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("GitHub API response must be an object");
  }
  return value as Record<string, unknown>;
}

async function request(url: string, token: string, fetchImpl: Fetch): Promise<Response> {
  // Never include response bodies, request headers or transport errors in diagnostics.
  let response: Response;
  try {
    response = await fetchImpl(url, {
      redirect: "error",
      signal: AbortSignal.timeout(30_000),
      headers: {
        Accept: "application/vnd.github+json",
        Authorization: `Bearer ${token}`,
        "X-GitHub-Api-Version": API_VERSION,
        "User-Agent": "gh-actions-quota",
      },
    });
  } catch {
    throw new Error("GitHub API request failed");
  }
  return response;
}

export async function requestJson(url: string, token: string, fetchImpl: Fetch = fetch): Promise<Record<string, unknown>> {
  const response = await request(url, token, fetchImpl);
  if (!response.ok) throw new Error(`GitHub API returned HTTP ${response.status}`);
  try {
    return object(await response.json());
  } catch {
    throw new Error("GitHub API returned an invalid response");
  }
}

async function isPublicRepository(repository: string, token: string, fetchImpl: Fetch): Promise<boolean> {
  const [owner, name] = repository.split("/", 2);
  if (!owner || !name) return false;
  const response = await request(
    `https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
    token, fetchImpl,
  );
  if (response.status === 404) return false;
  if (!response.ok) throw new Error("GitHub repository lookup failed");
  try {
    return object(await response.json()).private === false;
  } catch {
    throw new Error("GitHub API returned an invalid response");
  }
}

export async function parseUsedMinutes(payload: unknown, token: string, fetchImpl: Fetch = fetch): Promise<number> {
  const report = object(payload);
  if (!Array.isArray(report.usageItems)) {
    throw new Error("GitHub billing response must contain usageItems");
  }
  const publicRepositories = new Map<string, boolean>();
  let discountedUSD = 0;
  for (const candidate of report.usageItems) {
    const item = object(candidate);
    if (
      item.product !== "Actions" ||
      item.unitType !== "minutes" ||
      !INCLUDED_SKUS.has(normalizeSku(item.sku)) ||
      typeof item.repositoryName !== "string"
    ) continue;
    if (!publicRepositories.has(item.repositoryName)) {
      publicRepositories.set(item.repositoryName, await isPublicRepository(item.repositoryName, token, fetchImpl));
    }
    if (publicRepositories.get(item.repositoryName)) continue;
    if (typeof item.discountAmount !== "number" || !Number.isFinite(item.discountAmount) || item.discountAmount < 0) {
      throw new Error("Invalid discountAmount");
    }
    discountedUSD += item.discountAmount;
  }
  const minutes = discountedUSD / LINUX_MINUTE_PRICE_USD;
  if (!Number.isFinite(minutes)) throw new Error("GitHub billing usage is out of range");
  return minutes;
}

export async function fetchOwnerType(owner: string, token: string, fetchImpl: Fetch = fetch): Promise<OwnerType> {
  const account = await requestJson(`https://api.github.com/users/${encodeURIComponent(owner)}`, token, fetchImpl);
  const type = String(account.type || "").toLowerCase();
  if (type !== "user" && type !== "organization") throw new Error("Unsupported GitHub owner type");
  return type;
}

export async function fetchAuthenticatedUser(token: string, fetchImpl: Fetch = fetch): Promise<Record<string, unknown>> {
  return requestJson("https://api.github.com/user", token, fetchImpl);
}

export async function fetchPlan(owner: string, ownerType: OwnerType, token: string, fetchImpl: Fetch = fetch): Promise<string> {
  if (ownerType !== "user") throw new Error("Organization billing is not supported in v1");
  const account = await fetchAuthenticatedUser(token, fetchImpl);
  if (typeof account.login !== "string" || account.login.toLowerCase() !== owner.toLowerCase()) {
    throw new Error("Quota token does not belong to the repository owner");
  }
  const plan = account.plan ? object(account.plan) : undefined;
  if (typeof plan?.name !== "string" || !plan.name) {
    throw new Error("GitHub did not return the account plan; set quota-minutes explicitly");
  }
  return plan.name.toLowerCase();
}

export function includedMinutesForPlan(plan: string): number {
  const minutes = INCLUDED_MINUTES_BY_PLAN[plan.trim().toLowerCase()];
  if (!minutes) throw new Error("Unsupported GitHub plan; set quota-minutes explicitly");
  return minutes;
}

export async function fetchUsedMinutes(
  owner: string,
  token: string,
  { ownerType, now = new Date(), fetchImpl = fetch }: { ownerType?: OwnerType; now?: Date; fetchImpl?: Fetch } = {},
): Promise<number> {
  const type = ownerType || await fetchOwnerType(owner, token, fetchImpl);
  if (type !== "user") throw new Error("Organization billing is not supported in v1");
  const query = new URLSearchParams({
    year: String(now.getUTCFullYear()),
    month: String(now.getUTCMonth() + 1),
    product: "Actions",
  });
  const payload = await requestJson(
    `https://api.github.com/users/${encodeURIComponent(owner)}/settings/billing/usage?${query}`,
    token, fetchImpl,
  );
  return parseUsedMinutes(payload, token, fetchImpl);
}
