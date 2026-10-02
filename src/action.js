import { readFileSync, appendFileSync } from "node:fs";
import {
  fetchOwnerType,
  fetchPlan,
  fetchUsedMinutes,
  includedMinutesForPlan,
} from "./billing.js";
import { DEFAULT_THRESHOLD_PERCENT } from "./constants.js";
import { calculateUsage, parsePositiveNumber, parseThreshold } from "./usage.js";

function input(name) {
  return process.env[`INPUT_${name.toUpperCase().replaceAll("-", "_")}`] || "";
}

function setOutput(name, value) {
  const text = String(value);
  if (process.env.GITHUB_OUTPUT) {
    appendFileSync(process.env.GITHUB_OUTPUT, `${name}=${text}\n`, "utf8");
  } else {
    console.log(`${name}=${text}`);
  }
}

function summary(markdown) {
  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown, "utf8");
  }
}

function publicRepositoryContext() {
  if (process.env.GITHUB_REPOSITORY_VISIBILITY === "public") return true;
  if (!process.env.GITHUB_EVENT_PATH) return false;
  try {
    const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
    return event?.repository?.private === false;
  } catch {
    return false;
  }
}

function writeResult(result) {
  for (const [name, value] of Object.entries(result)) setOutput(name, value);
}

async function main() {
  const threshold = parseThreshold(input("threshold") || DEFAULT_THRESHOLD_PERCENT);

  if (publicRepositoryContext()) {
    writeResult({
      "usage-available": true,
      "used-minutes": 0,
      "quota-minutes": "unmetered",
      "usage-percent": 0,
      "remaining-minutes": "unmetered",
      allowed: true,
      "billing-owner": process.env.GITHUB_REPOSITORY_OWNER || "unknown",
      "billing-owner-type": "unmetered",
      unmetered: true,
    });
    summary(
      "## GitHub Actions quota\n\nStandard GitHub-hosted runners are unmetered for this public repository.\n",
    );
    return;
  }

  const owner = process.env.GITHUB_REPOSITORY_OWNER;
  const token = input("token") || process.env.ACTIONS_QUOTA_TOKEN;
  if (!owner) throw new Error("GITHUB_REPOSITORY_OWNER is unavailable");
  if (!token) {
    throw new Error(
      "Missing token. Pass token: ${{ secrets.ACTIONS_QUOTA_TOKEN }} to the action.",
    );
  }

  const ownerType = await fetchOwnerType(owner, token);
  const explicitQuota = input("quota-minutes");
  const quotaMinutes = explicitQuota
    ? parsePositiveNumber(explicitQuota, "quota-minutes")
    : includedMinutesForPlan(await fetchPlan(owner, ownerType, token));
  const usedMinutes = await fetchUsedMinutes(owner, token, { ownerType });
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
    unmetered: false,
  });

  summary(
    `## GitHub Actions quota\n\nUsage: **${formatNumber(usage.usedMinutes)} / ${formatNumber(usage.quotaMinutes)} minutes (${formatNumber(usage.usagePercent)}%)**\n\nThreshold: **${formatNumber(threshold)}%**\n\nAllowed: **${usage.allowed}**\n`,
  );
}

function formatNumber(value) {
  return Number(value.toFixed(6)).toString();
}

main().catch((error) => {
  const message = String(error?.message || error).replace(/[\r\n]+/g, " ");
  console.log(`::warning title=Actions quota::${message}`);
  writeResult({
    "usage-available": false,
    "used-minutes": "unavailable",
    "quota-minutes": input("quota-minutes") || "unavailable",
    "usage-percent": "unavailable",
    "remaining-minutes": "unavailable",
    allowed: false,
    "billing-owner": process.env.GITHUB_REPOSITORY_OWNER || "unknown",
    "billing-owner-type": "unavailable",
    unmetered: false,
  });
  summary(`## GitHub Actions quota\n\nUsage unavailable.\n\n${message}\n`);
});
