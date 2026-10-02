export const API_VERSION = "2026-03-10";
export const LINUX_MINUTE_PRICE_USD = 0.006;
export const DEFAULT_THRESHOLD_PERCENT = 50;
export const ACTIONS_QUOTA_SECRET = "ACTIONS_QUOTA_TOKEN";

// Set this once the public GitHub App is registered. Until then, setup can be
// tested with GITHUB_ACTIONS_QUOTA_CLIENT_ID in the environment.
export const DEFAULT_GITHUB_APP_CLIENT_ID = "";

export const INCLUDED_MINUTES_BY_PLAN = Object.freeze({
  free: 2000,
  pro: 3000,
  team: 3000,
  enterprise: 50000,
  "enterprise cloud": 50000,
});
