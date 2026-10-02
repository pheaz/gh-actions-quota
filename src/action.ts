import { readFileSync, appendFileSync } from "node:fs";
import { fetchOwnerType, fetchPlan, fetchUsedMinutes, includedMinutesForPlan, type Fetch } from "./billing.js";
import { DEFAULT_THRESHOLD_PERCENT } from "./constants.js";
import { calculateUsage, parsePositiveNumber, parseThreshold } from "./usage.js";

type Result = Record<string, string | number | boolean>;

export async function runAction(env: NodeJS.ProcessEnv = process.env, fetchImpl: Fetch = fetch): Promise<void> {
  // The runner's INPUT_* names preserve hyphens (including INPUT_QUOTA-MINUTES).
  // Retain the underscore spelling as a compatibility fallback.
  const input = (name: string): string => env[`INPUT_${name.toUpperCase()}`] || env[`INPUT_${name.toUpperCase().replaceAll("-", "_")}`] || "";
  const summary = (markdown: string): void => {
    if (env.GITHUB_STEP_SUMMARY) appendFileSync(env.GITHUB_STEP_SUMMARY, markdown, "utf8");
  };
  const writeResult = (result: Result): void => {
    for (const [name, value] of Object.entries(result)) {
      const line = `${name}=${String(value).replace(/[\r\n]/g, " ")}\n`;
      if (env.GITHUB_OUTPUT) appendFileSync(env.GITHUB_OUTPUT, line, "utf8");
      else console.log(line.trimEnd());
    }
  };

  try {
    const threshold = parseThreshold(input("threshold") || DEFAULT_THRESHOLD_PERCENT);
    if (publicRepositoryContext(env)) {
      writeResult({
        "usage-available": true, "used-minutes": 0, "quota-minutes": "unmetered",
        "usage-percent": 0, "remaining-minutes": "unmetered", allowed: true,
        "billing-owner": env.GITHUB_REPOSITORY_OWNER || "unknown", "billing-owner-type": "unmetered", unmetered: true,
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
    const quotaMinutes = explicitQuota
      ? parsePositiveNumber(explicitQuota, "quota-minutes")
      : includedMinutesForPlan(await fetchPlan(owner, ownerType, token, fetchImpl));
    const usedMinutes = await fetchUsedMinutes(owner, token, { ownerType, fetchImpl });
    const usage = calculateUsage(usedMinutes, quotaMinutes, threshold);
    writeResult({
      "usage-available": true, "used-minutes": formatNumber(usage.usedMinutes),
      "quota-minutes": formatNumber(usage.quotaMinutes), "usage-percent": formatNumber(usage.usagePercent),
      "remaining-minutes": formatNumber(usage.remainingMinutes), allowed: usage.allowed,
      "billing-owner": owner, "billing-owner-type": ownerType, unmetered: false,
    });
    summary(`## GitHub Actions quota\n\nUsage: **${formatNumber(usage.usedMinutes)} / ${formatNumber(usage.quotaMinutes)} minutes (${formatNumber(usage.usagePercent)}%)**\n\nThreshold: **${formatNumber(threshold)}%**\n\nAllowed: **${usage.allowed}**\n`);
  } catch {
    // Fail closed without printing exceptions that could contain credentials.
    const message = "Billing usage unavailable. Check the token, account plan, billing access and action inputs.";
    console.log(`::warning title=Actions quota::${message}`);
    writeResult({
      "usage-available": false, "used-minutes": "unavailable", "quota-minutes": input("quota-minutes") || "unavailable",
      "usage-percent": "unavailable", "remaining-minutes": "unavailable", allowed: false,
      "billing-owner": env.GITHUB_REPOSITORY_OWNER || "unknown", "billing-owner-type": "unavailable", unmetered: false,
    });
    summary(`## GitHub Actions quota\n\nUsage unavailable.\n\n${message}\n`);
  }
}

function publicRepositoryContext(env: NodeJS.ProcessEnv): boolean {
  if (env.GITHUB_REPOSITORY_VISIBILITY === "public") return true;
  if (!env.GITHUB_EVENT_PATH) return false;
  try {
    const event = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8")) as { repository?: { private?: boolean } } | null;
    return event?.repository?.private === false;
  } catch { return false; }
}

function formatNumber(value: number): string {
  return Number(value.toFixed(6)).toString();
}
