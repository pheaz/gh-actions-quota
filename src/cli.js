#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import {
  fetchAuthenticatedUser,
  fetchOwnerType,
  fetchPlan,
  fetchUsedMinutes,
  includedMinutesForPlan,
} from "./billing.js";
import {
  ACTIONS_QUOTA_SECRET,
  DEFAULT_GITHUB_APP_CLIENT_ID,
} from "./constants.js";
import { openBrowser, pollForUserToken, requestDeviceCode } from "./device-flow.js";

const command = process.argv[2] || "help";

try {
  if (command === "setup") {
    await setup();
  } else if (command === "help" || command === "--help" || command === "-h") {
    help();
  } else {
    throw new Error(`Unknown command: ${command}`);
  }
} catch (error) {
  console.error(`\nError: ${error?.message || error}`);
  process.exitCode = 1;
}

async function setup() {
  requireCommand("gh", ["--version"]);
  requireCommand("gh", ["auth", "status"]);

  const repo = run("gh", [
    "repo",
    "view",
    "--json",
    "nameWithOwner",
    "--jq",
    ".nameWithOwner",
  ]).trim();
  if (!repo.includes("/")) throw new Error("Could not resolve the current GitHub repository");
  const [owner] = repo.split("/");

  const clientId =
    process.env.GITHUB_ACTIONS_QUOTA_CLIENT_ID || DEFAULT_GITHUB_APP_CLIENT_ID;
  if (!clientId) {
    throw new Error(
      "GitHub App client ID is not configured yet. Set GITHUB_ACTIONS_QUOTA_CLIENT_ID while developing this release.",
    );
  }

  console.log(`Repository: ${repo}`);
  console.log("Requesting read-only GitHub plan access...");

  const device = await requestDeviceCode(clientId);
  console.log(`\nCode: ${device.user_code}`);
  console.log(`Open: ${device.verification_uri}`);
  if (openBrowser(device.verification_uri)) console.log("Browser opened.");
  console.log("Waiting for GitHub authorization...");

  const token = await pollForUserToken(clientId, device);
  const account = await fetchAuthenticatedUser(token);
  if (String(account.login || "").toLowerCase() !== owner.toLowerCase()) {
    throw new Error(
      `Authorized GitHub account ${account.login} does not own ${repo}. Run setup as ${owner}.`,
    );
  }

  const ownerType = await fetchOwnerType(owner, token);
  if (ownerType !== "user") {
    throw new Error(
      "Automatic setup currently supports repositories owned by personal GitHub accounts. Organization billing requires separate organization permissions.",
    );
  }

  const plan = await fetchPlan(owner, ownerType, token);
  const quota = includedMinutesForPlan(plan);
  const used = await fetchUsedMinutes(owner, token, { ownerType });

  const result = spawnSync(
    "gh",
    ["secret", "set", ACTIONS_QUOTA_SECRET, "--repo", repo],
    {
      input: token,
      encoding: "utf8",
      stdio: ["pipe", "pipe", "pipe"],
    },
  );
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || "gh secret set failed");
  }

  console.log(`\nAuthorized: ${account.login}`);
  console.log(`Plan: ${plan}`);
  console.log(`Actions usage: ${formatNumber(used)} / ${quota} Linux-equivalent minutes`);
  console.log(`Stored repository secret: ${ACTIONS_QUOTA_SECRET}`);
  console.log("\nAdd this step to your workflow:\n");
  console.log("- uses: philippwallrafen/github-actions-quota@v1");
  console.log("  id: quota");
  console.log("  with:");
  console.log("    token: ${{ secrets.ACTIONS_QUOTA_TOKEN }}");
  console.log("    threshold: 50");
}

function help() {
  console.log(`github-actions-quota\n\nUsage:\n  github-actions-quota setup\n\nsetup authorizes the github-actions-quota GitHub App with read-only Plan access and stores the resulting non-expiring user token as ACTIONS_QUOTA_TOKEN in the current repository.\n`);
}

function run(commandName, args) {
  const result = spawnSync(commandName, args, { encoding: "utf8" });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || `${commandName} ${args.join(" ")} failed`);
  }
  return result.stdout;
}

function requireCommand(commandName, args) {
  run(commandName, args);
}

function formatNumber(value) {
  return Number(Number(value).toFixed(2)).toString();
}
