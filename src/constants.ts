export const API_VERSION = "2026-03-10";
export const LINUX_BASE_PRICE_USD = 0.006;
export const DEFAULT_THRESHOLD_PERCENT = 50;
export const INCLUDED_MINUTES_BY_PLAN: Readonly<Record<string, number>> = Object.freeze({
  free: 2000,
  pro: 3000,
  team: 3000,
  enterprise: 50000,
  "enterprise cloud": 50000,
});
