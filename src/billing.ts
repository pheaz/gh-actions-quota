import { API_VERSION, INCLUDED_MINUTES_BY_PLAN, LINUX_BASE_PRICE_USD } from "./constants.js";

export type Fetch = typeof fetch;
export type OwnerType = "user" | "organization";

// Quota usage is normalized to the standard Linux 2-core rate:
// effective minutes = runtime minutes × (SKU price / $0.006).
// Keep prices and the corresponding Linux-equivalent factors visible here
// because they define the quota estimate.
const SKU_PRICE_PER_MINUTE_USD: Readonly<Record<string, number>> = {
  actions_linux_slim: 0.002, // 0.3333× Linux
  actions_linux_arm: 0.005, // 0.8333× Linux
  actions_linux: 0.006, // 1.0000× Linux
  actions_windows: 0.010, // 1.6667× Linux
  actions_windows_arm: 0.010, // 1.6667× Linux
  actions_macos: 0.062, // 10.3333× Linux
} as const;

function normalizeSku(sku: unknown): string {
  const normalized = typeof sku === "string" ? sku.trim().toLowerCase().replace(/[\s-]+/g, "_") : "";
  // Standard macOS runners have 3 or 4 cores; larger core counts stay excluded.
  return normalized === "actions_macos_3_core" || normalized === "actions_macos_4_core" ? "actions_macos" : normalized;
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

async function isPrivateRepository(repository: string, token: string, fetchImpl: Fetch): Promise<boolean> {
  const [owner, name] = repository.split("/", 2);
  if (!owner || !name) return false;
  const response = await request(
    `https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
    token, fetchImpl,
  );
  if (response.status === 404) return false;
  if (!response.ok) throw new Error("GitHub repository lookup failed");
  try {
    return object(await response.json()).private === true;
  } catch {
    throw new Error("GitHub API returned an invalid response");
  }
}

export async function parseUsedMinutes(payload: unknown, token: string, fetchImpl: Fetch = fetch): Promise<number> {
  const report = object(payload);
  if (!Array.isArray(report.usageItems)) {
    throw new Error("GitHub billing response must contain usageItems");
  }
  // Visibility is current; historical public/private changes are not reflected here.
  const privateRepositories = new Map<string, boolean>();
  let usedMinutes = 0;
  for (const candidate of report.usageItems) {
    const item = object(candidate);
    const skuPrice = SKU_PRICE_PER_MINUTE_USD[normalizeSku(item.sku)];
    if (
      typeof item.product !== "string" || item.product.toLowerCase() !== "actions" ||
      typeof item.unitType !== "string" || item.unitType.toLowerCase() !== "minutes" ||
      typeof skuPrice !== "number" ||
      typeof item.repositoryName !== "string"
    ) continue;
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
  const endpoint = type === "organization" ? "organizations" : "users";
  const query = new URLSearchParams({
    year: String(now.getUTCFullYear()),
    month: String(now.getUTCMonth() + 1),
    product: "Actions",
  });
  const payload = await requestJson(
    `https://api.github.com/${endpoint}/${encodeURIComponent(owner)}/settings/billing/usage?${query}`,
    token, fetchImpl,
  );
  return parseUsedMinutes(payload, token, fetchImpl);
}
