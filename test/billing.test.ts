import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { API_VERSION } from "../src/constants.js";
import { fetchPlan, fetchUsedMinutes, includedMinutesForPlan, parseUsedMinutes } from "../src/billing.js";

const fakeToken = "test-only-token";
const privateRepository: typeof fetch = async () => new Response(JSON.stringify({ private: true }));
function item(quantity: unknown, overrides: Record<string, unknown> = {}) {
  return { product: "actions", unitType: "Minutes", sku: "actions_linux", repositoryName: "owner/private", quantity,
    pricePerUnit: 0.006, grossAmount: 3, discountAmount: 0, netAmount: 3, ...overrides };
}

function assertMinutes(actual: number, expected: number) {
  assert.ok(Number.isFinite(actual) && Math.abs(actual - expected) <= 1e-8, `used=${actual}, want ${expected}`);
}

// The Go CLI reads this same corpus so both implementations keep the same contract.
const billingCases: {
  name: string;
  ownerType?: "user" | "organization";
  usageItems: unknown[];
  repositories: Record<string, { status: number; private?: boolean }>;
  usedMinutes?: number;
  error?: string;
  lookups: Record<string, number>;
}[] = JSON.parse(readFileSync("test/fixtures/billing.json", "utf8"));

for (const fixture of billingCases) {
  test(`shared billing contract: ${fixture.name}`, async () => {
    const lookups: Record<string, number> = {};
    const fetchImpl: typeof fetch = async url => {
      const request = new URL(String(url));
      const endpoint = fixture.ownerType === "organization" ? "organizations" : "users";
      if (request.pathname === `/${endpoint}/owner/settings/billing/usage`) {
        assert.equal(request.searchParams.get("year"), "2026");
        assert.equal(request.searchParams.get("month"), "8");
        assert.equal(request.searchParams.get("product"), "Actions");
        return new Response(JSON.stringify({ usageItems: fixture.usageItems }));
      }
      const repository = request.pathname.replace(/^\/repos\//, "");
      const response = fixture.repositories[repository];
      assert.ok(response, `unexpected endpoint: ${request.pathname}`);
      lookups[repository] = (lookups[repository] || 0) + 1;
      return new Response(JSON.stringify({ private: response.private }), { status: response.status });
    };
    const used = () => fetchUsedMinutes("owner", fakeToken, {
      ownerType: fixture.ownerType || "user", now: new Date("2026-09-01T00:30:00+02:00"), fetchImpl,
    });
    if (fixture.error) {
      await assert.rejects(used, { message: fixture.error });
    } else {
      assertMinutes(await used(), fixture.usedMinutes!);
    }
    assert.deepEqual(lookups, fixture.lookups);
  });
}

test("mixed runner runtime normalizes to Linux-equivalent minutes", async () => {
  const used = await parseUsedMinutes({ usageItems: [
    item(120, { sku: "Actions Windows" }),
    item(174.193548387, { sku: " actions-macos " }),
    item(99, { product: "Packages" }),
  ] }, fakeToken, privateRepository);
  assertMinutes(used, 2000);
});

test("500 private and 3000 public quota minutes count as 500 with cached lookups", async () => {
  const requests: string[] = [];
  const fetchImpl: typeof fetch = async (url, init) => {
    requests.push(String(url));
    assert.deepEqual(init?.headers, {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${fakeToken}`,
      "X-GitHub-Api-Version": API_VERSION,
      "User-Agent": "gh-actions-quota",
    });
    return new Response(JSON.stringify({ private: String(url).endsWith("/private") }));
  };
  const used = await parseUsedMinutes({ usageItems: [
    item(200), item(300),
    item(1000, { repositoryName: "owner/public" }),
    item(1200, { repositoryName: "owner/public", sku: "actions_windows" }),
    item("ignored", { repositoryName: "owner/public" }),
  ] }, fakeToken, fetchImpl);
  assert.equal(used, 500);
  assert.deepEqual(requests, ["https://api.github.com/repos/owner/private", "https://api.github.com/repos/owner/public"]);
});

test("404 repositories are excluded and cached", async () => {
  let lookups = 0;
  const fetchImpl: typeof fetch = async () => {
    lookups++;
    return new Response(fakeToken, { status: 404 });
  };
  assert.equal(await parseUsedMinutes({ usageItems: [item(200), item(300)] }, fakeToken, fetchImpl), 0);
  assert.equal(lookups, 1);
});

test("larger runners, storage, self-hosted and unrelated items are ignored before lookup", async () => {
  const fetchImpl: typeof fetch = async () => { throw new Error("unexpected repository lookup"); };
  const usageItems = [
    ...["actions_linux_4_core", "Actions Windows 8 Core", "Actions macOS 12-core", "actions_storage", "actions_self_hosted", "unknown", "constructor", null]
      .map(sku => item("invalid", { sku })),
    item("invalid", { product: "Packages" }),
    item("invalid", { unitType: "gigabytes" }),
    item("invalid", { repositoryName: null }),
    item("invalid", { product: {} }),
    item("invalid", { unitType: [] }),
  ];
  assert.equal(await parseUsedMinutes({ usageItems }, fakeToken, fetchImpl), 0);
});

for (const [sku, expected] of [
  ["actions_linux_slim", 33.33333333333333], ["actions_linux_arm", 83.33333333333333],
  ["actions_linux", 100], ["actions_windows", 166.66666666666666],
  ["actions_windows_arm", 166.66666666666666], ["actions_macos", 1033.3333333333333],
  [" Actions Linux Slim ", 33.33333333333333], ["Actions-Linux-ARM", 83.33333333333333],
] as const) {
  test(`standard SKU ${sku} is included`, async () => {
    assertMinutes(await parseUsedMinutes({ usageItems: [item(100, { sku })] }, fakeToken, privateRepository), expected);
  });
}

test("fetchUsedMinutes chooses the detailed user billing endpoint in the UTC month", async () => {
  const requests: string[] = [];
  const fetchImpl: typeof fetch = async (url) => {
    requests.push(String(url));
    return new Response(JSON.stringify(String(url).includes("/billing/") ? { usageItems: [item(300)] } : { private: true }));
  };
  assert.equal(await fetchUsedMinutes("owner/name", fakeToken, {
    ownerType: "user", now: new Date("2026-09-01T00:30:00+02:00"), fetchImpl,
  }), 300);
  assert.deepEqual(requests, [
    "https://api.github.com/users/owner%2Fname/settings/billing/usage?year=2026&month=8&product=Actions",
    "https://api.github.com/repos/owner/private",
  ]);
});

test("empty month has zero usage", async () => {
  assert.equal(await parseUsedMinutes({ usageItems: [] }, fakeToken, privateRepository), 0);
});

for (const payload of [null, [], {}, { usageItems: {} }, { usageItems: [null] }]) {
  test(`reject invalid billing data ${JSON.stringify(payload)}`, async () => {
    await assert.rejects(() => parseUsedMinutes(payload, fakeToken, privateRepository));
  });
}

for (const quantity of [undefined, null, "6", -1, NaN, Infinity, -Infinity]) {
  test(`reject invalid quantity ${String(quantity)}`, async () => {
    await assert.rejects(() => parseUsedMinutes({ usageItems: [item(quantity)] }, fakeToken, privateRepository), /Invalid quantity/);
  });
}

test("out-of-range weighted or summed usage is rejected", async () => {
  for (const usageItems of [[item(Number.MAX_VALUE, { sku: "actions_macos" })], [item(Number.MAX_VALUE), item(Number.MAX_VALUE)]]) {
    await assert.rejects(() => parseUsedMinutes({ usageItems }, fakeToken, privateRepository), /out of range/);
  }
});

for (const status of [401, 403, 500]) {
  test(`repository HTTP ${status} fails without leaking the response`, async () => {
    const fetchImpl: typeof fetch = async () => new Response(fakeToken, { status });
    await assert.rejects(() => parseUsedMinutes({ usageItems: [item(3)] }, fakeToken, fetchImpl), /^Error: GitHub repository lookup failed$/);
  });
}

test("unavailable repository visibility and malformed names are excluded", async () => {
  const fetchImpl: typeof fetch = async () => new Response(JSON.stringify({}));
  assert.equal(await parseUsedMinutes({ usageItems: [item(500)] }, fakeToken, fetchImpl), 0);
  const noLookup: typeof fetch = async () => { throw new Error("unexpected lookup"); };
  assert.equal(await parseUsedMinutes({ usageItems: [item(500, { repositoryName: "missing-owner" })] }, fakeToken, noLookup), 0);
});

test("repository URL components are encoded", async () => {
  let seenUrl = "";
  const fetchImpl: typeof fetch = async url => {
    seenUrl = String(url);
    return new Response(JSON.stringify({ private: true }));
  };
  await parseUsedMinutes({ usageItems: [item(3, { repositoryName: "an owner/a#repo" })] }, fakeToken, fetchImpl);
  assert.equal(seenUrl, "https://api.github.com/repos/an%20owner/a%23repo");
});

test("network failure emits a sanitized error", async () => {
  const fetchImpl: typeof fetch = async () => { throw new Error(`${fakeToken} transport dump`); };
  await assert.rejects(() => fetchUsedMinutes("owner", fakeToken, { ownerType: "user", fetchImpl }), /^Error: GitHub API request failed$/);
  await assert.rejects(() => parseUsedMinutes({ usageItems: [item(3)] }, fakeToken, fetchImpl), /^Error: GitHub API request failed$/);
});

test("plan names map to current included Actions minutes", () => {
  assert.equal(includedMinutesForPlan("free"), 2000);
  assert.equal(includedMinutesForPlan("pro"), 3000);
  assert.equal(includedMinutesForPlan("team"), 3000);
  assert.equal(includedMinutesForPlan("enterprise"), 50000);
  assert.equal(includedMinutesForPlan(" enterprise cloud "), 50000);
  assert.equal(includedMinutesForPlan("PRO"), 3000);
  assert.throws(() => includedMinutesForPlan("mystery"), /Unsupported GitHub plan/);
});

test("fetchPlan rejects a token for a different personal account", async () => {
  const fetchImpl: typeof fetch = async () => new Response(JSON.stringify({ login: "other", plan: { name: "pro" } }));
  await assert.rejects(() => fetchPlan("owner", "user", fakeToken, fetchImpl), /repository owner/i);
});
