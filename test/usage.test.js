import assert from "node:assert/strict";
import test from "node:test";
import { calculateUsage, parseThreshold } from "../src/usage.js";

test("calculateUsage gates at the configured threshold", () => {
  const below = calculateUsage(1499, 3000, 50);
  assert.equal(below.allowed, true);
  assert.equal(below.remainingMinutes, 1501);

  const atLimit = calculateUsage(1500, 3000, 50);
  assert.equal(atLimit.allowed, false);
  assert.equal(atLimit.usagePercent, 50);
});

test("calculateUsage validates quota and threshold", () => {
  assert.throws(() => calculateUsage(1, 0, 50), /quota-minutes/);
  assert.throws(() => parseThreshold(0), /threshold/);
  assert.throws(() => parseThreshold(101), /threshold/);
});
