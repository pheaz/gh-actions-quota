import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runAction } from "../src/action.js";

// Deliberately fake credentials; all requests are mocked.
const fakeToken = "test-only-token";
async function action(options: { env?: NodeJS.ProcessEnv; used?: number; plan?: string; status?: number; ownerType?: string; malformed?: boolean } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "actions-quota-test-"));
  const output = join(dir, "output");
  const summary = join(dir, "summary");
  const event = join(dir, "event.json");
  writeFileSync(event, JSON.stringify({ repository: { private: true } }));
  const env: NodeJS.ProcessEnv = {
    GITHUB_REPOSITORY_OWNER: "owner", GITHUB_ACTOR: "other-actor",
    INPUT_TOKEN: fakeToken, GITHUB_EVENT_PATH: event, GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: summary,
    ...options.env,
  };
  const requests: string[] = [];
  const fetchImpl: typeof fetch = async (url, init) => {
    requests.push(String(url));
    assert.equal((init?.headers as Record<string, string>).Authorization, `Bearer ${fakeToken}`);
    if (options.status) return new Response(fakeToken, { status: options.status });
    const body = String(url).endsWith("/user")
      ? { login: "owner", type: "User", plan: { name: options.plan || "pro" } }
      : String(url).includes("/billing/")
        ? { usageItems: [{ product: "Actions", unitType: "minutes", discountAmount: options.malformed ? "invalid" : (options.used ?? 1200) * 0.006 }] }
        : { type: options.ownerType || "User" };
    return new Response(JSON.stringify(body));
  };
  try {
    await runAction(env, fetchImpl);
    const result = Object.fromEntries(readFileSync(output, "utf8").trim().split("\n").map(line => line.split("=")));
    const markdown = readFileSync(summary, "utf8");
    assert.ok(!markdown.includes(fakeToken));
    return { result, requests, markdown };
  } finally { rmSync(dir, { recursive: true, force: true }); }
}

for (const [used, allowed] of [[1499, "true"], [1500, "false"], [1501, "false"]] as const) {
  test(`private action at ${used} minutes: allowed=${allowed}`, async () => {
    const { result, requests } = await action({ used });
    assert.equal(result.allowed, allowed);
    assert.equal(result["usage-available"], "true");
    assert.equal(result["billing-owner"], "owner");
    assert.equal(result["billing-owner-type"], "user");
    assert.equal(result.unmetered, "false");
    assert.equal(result["quota-minutes"], "3000");
    assert.equal(result["remaining-minutes"], String(3000 - used));
    assert.equal(requests.some(url => url.includes("other-actor")), false);
  });
}

test("public repo is unmetered without token and bypasses API", async () => {
  const { result, requests } = await action({ env: { INPUT_TOKEN: "", GITHUB_REPOSITORY_VISIBILITY: "public" } });
  assert.equal(result.allowed, "true");
  assert.equal(result["usage-available"], "true");
  assert.equal(result.unmetered, "true");
  assert.equal(result["quota-minutes"], "unmetered");
  assert.deepEqual(requests, []);
});

test("public repository event enables the same shortcut", async () => {
  const dir = mkdtempSync(join(tmpdir(), "quota-public-"));
  try {
    const path = join(dir, "event.json");
    writeFileSync(path, JSON.stringify({ repository: { private: false } }));
    const { result, requests } = await action({ env: { INPUT_TOKEN: "", GITHUB_EVENT_PATH: path } });
    assert.equal(result.unmetered, "true");
    assert.deepEqual(requests, []);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("private repo without token fails closed", async () => {
  const { result, requests } = await action({ env: { INPUT_TOKEN: "" } });
  assert.equal(result.allowed, "false");
  assert.equal(result["usage-available"], "false");
  assert.equal(result["used-minutes"], "unavailable");
  assert.deepEqual(requests, []);
});

for (const status of [401, 403, 500]) {
  test(`API HTTP ${status} fails closed without leaking the body`, async () => {
    const { result } = await action({ status });
    assert.equal(result.allowed, "false");
    assert.equal(result["usage-available"], "false");
  });
}

test("billing schema failure fails closed", async () => {
  const { result } = await action({ malformed: true });
  assert.equal(result.allowed, "false");
  assert.equal(result["usage-available"], "false");
});

test("organization billing fails closed before plan/billing requests", async () => {
  const { result, requests } = await action({ ownerType: "Organization" });
  assert.equal(result.allowed, "false");
  assert.equal(requests.length, 1);
});

test("quota override uses the runner's hyphenated INPUT name and skips plan detection", async () => {
  const { result, requests } = await action({ env: { "INPUT_QUOTA-MINUTES": "4000" }, plan: "legacy" });
  assert.equal(result["quota-minutes"], "4000");
  assert.equal(result["usage-percent"], "30");
  assert.equal(result.allowed, "true");
  assert.equal(requests.some(url => url.endsWith("/user")), false);
});

test("environment token fallback and custom threshold remain supported", async () => {
  const { result } = await action({ env: { INPUT_TOKEN: "", ACTIONS_QUOTA_TOKEN: fakeToken, INPUT_THRESHOLD: "40" } });
  assert.equal(result.allowed, "false");
});

for (const env of [{ INPUT_THRESHOLD: "invalid" }, { "INPUT_QUOTA-MINUTES": "0" }, { GITHUB_REPOSITORY_OWNER: "" }]) {
  test(`invalid configuration ${Object.keys(env)[0]} fails closed`, async () => {
    const { result } = await action({ env });
    assert.equal(result.allowed, "false");
    assert.equal(result["usage-available"], "false");
  });
}
