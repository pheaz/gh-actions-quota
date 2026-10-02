import assert from "node:assert/strict";
import test from "node:test";
import {
  fetchPlan,
  fetchUsedMinutes,
  includedMinutesForPlan,
  parseUsedMinutes,
} from "../src/billing.js";

test("mixed runner discounts normalize to Linux-equivalent minutes", () => {
  const used = parseUsedMinutes({
    usageItems: [
      { product: "Actions", unitType: "minutes", discountAmount: 6.13 },
      { product: "Actions", unitType: "minutes", discountAmount: 5.87 },
      { product: "Packages", unitType: "minutes", discountAmount: 99 },
    ],
  });
  assert.equal(used, 2000);
});

test("plan names map to current included Actions minutes", () => {
  assert.equal(includedMinutesForPlan("free"), 2000);
  assert.equal(includedMinutesForPlan("pro"), 3000);
  assert.equal(includedMinutesForPlan("team"), 3000);
  assert.equal(includedMinutesForPlan("enterprise"), 50000);
  assert.throws(() => includedMinutesForPlan("mystery"), /Unsupported GitHub plan/);
});

test("fetchUsedMinutes chooses the user billing endpoint", async () => {
  let seenUrl: string | undefined;
  const fetchImpl: typeof fetch = async (url) => {
    seenUrl = String(url);
    return new Response(
      JSON.stringify({
        usageItems: [
          { product: "Actions", unitType: "minutes", discountAmount: 1.8 },
        ],
      }),
      { status: 200 },
    );
  };

  const used = await fetchUsedMinutes("owner/name", "token", {
    ownerType: "user",
    now: new Date("2026-08-11T00:00:00Z"),
    fetchImpl,
  });
  assert.equal(used, 300);
  assert.equal(
    seenUrl,
    "https://api.github.com/users/owner%2Fname/settings/billing/usage/summary?year=2026&month=8&product=Actions",
  );
});

test("fetchPlan rejects a token for a different personal account", async () => {
  const fetchImpl = async () =>
    new Response(JSON.stringify({ login: "other", plan: { name: "pro" } }), {
      status: 200,
    });

  await assert.rejects(
    () => fetchPlan("owner", "user", "token", fetchImpl),
    /repository owner/i,
  );
});

test("Enterprise Cloud and normalized plan names", () => {
  assert.equal(includedMinutesForPlan(" enterprise cloud "), 50000);
  assert.equal(includedMinutesForPlan("PRO"), 3000);
});

test("billing only includes minute discounts, including an empty month", () => {
  assert.equal(parseUsedMinutes({ usageItems: [] }), 0);
  assert.equal(parseUsedMinutes({ usageItems: [
    { product: "Actions", unitType: "minutes", discountAmount: 1.8, grossAmount: 900 },
    { product: "Actions", unitType: "gigabytes", discountAmount: 50 },
  ] }), 300);
});

for (const payload of [null, [], {}, { usageItems: {} }, { usageItems: [null] },
  { usageItems: [{ product: "Actions", unitType: "minutes", discountAmount: "6" }] },
  { usageItems: [{ product: "Actions", unitType: "minutes", discountAmount: -1 }] }]) {
  test(`reject invalid billing data ${JSON.stringify(payload)}`, () => {
    assert.throws(() => parseUsedMinutes(payload));
  });
}

test("network failure emits a sanitized error", async () => {
  const fetchImpl: typeof fetch = async () => { throw new Error("test-only-token transport dump"); };
  await assert.rejects(() => fetchUsedMinutes("owner", "test-only-token", { ownerType: "user", fetchImpl }), /^Error: GitHub API request failed$/);
});
