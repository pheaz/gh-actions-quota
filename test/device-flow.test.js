import assert from "node:assert/strict";
import test from "node:test";
import { pollForUserToken, requestDeviceCode } from "../src/device-flow.js";

test("requestDeviceCode sends the public GitHub App client ID", async () => {
  let body;
  const fetchImpl = async (_url, options) => {
    body = options.body;
    return new Response(
      JSON.stringify({
        device_code: "device",
        user_code: "ABCD-EFGH",
        verification_uri: "https://github.com/login/device",
        expires_in: 900,
        interval: 5,
      }),
      { status: 200 },
    );
  };
  const result = await requestDeviceCode("Iv1.client", fetchImpl);
  assert.match(body, /client_id=Iv1.client/);
  assert.equal(result.user_code, "ABCD-EFGH");
});

test("pollForUserToken accepts a non-expiring user access token", async () => {
  const fetchImpl = async () =>
    new Response(JSON.stringify({ access_token: "ghu_secret", token_type: "bearer" }), {
      status: 200,
    });
  const token = await pollForUserToken(
    "Iv1.client",
    { device_code: "device", expires_in: 900, interval: 1 },
    { fetchImpl, sleep: async () => {} },
  );
  assert.equal(token, "ghu_secret");
});

test("pollForUserToken refuses expiring tokens in repository-secret mode", async () => {
  const fetchImpl = async () =>
    new Response(
      JSON.stringify({
        access_token: "ghu_secret",
        expires_in: 28800,
        refresh_token: "ghr_refresh",
      }),
      { status: 200 },
    );
  await assert.rejects(
    () =>
      pollForUserToken(
        "Iv1.client",
        { device_code: "device", expires_in: 900, interval: 1 },
        { fetchImpl, sleep: async () => {} },
      ),
    /expiring user token/,
  );
});
