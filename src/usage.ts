import { DEFAULT_THRESHOLD_PERCENT } from "./constants.js";

export function parsePositiveNumber(value: string | number, label: string): number {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    throw new Error(`${label} must be positive and finite`);
  }
  return number;
}

export function parseThreshold(value: string | number = DEFAULT_THRESHOLD_PERCENT): number {
  const threshold = Number(value);
  if (!Number.isFinite(threshold) || threshold <= 0 || threshold > 100) {
    throw new Error("threshold must be greater than 0 and at most 100");
  }
  return threshold;
}

export function calculateUsage(
  usedMinutes: number,
  quotaMinutes: number,
  threshold = DEFAULT_THRESHOLD_PERCENT,
) {
  const used = Number(usedMinutes);
  const quota = parsePositiveNumber(quotaMinutes, "quota-minutes");
  const limit = parseThreshold(threshold);
  if (!Number.isFinite(used) || used < 0) {
    throw new Error("used minutes must be non-negative and finite");
  }

  const usagePercent = (used / quota) * 100;
  return {
    usedMinutes: used,
    quotaMinutes: quota,
    usagePercent,
    remainingMinutes: Math.max(0, quota - used),
    allowed: usagePercent < limit,
    thresholdPercent: limit,
  };
}
