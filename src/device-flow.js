import { spawn } from "node:child_process";

function formBody(values) {
  return new URLSearchParams(values).toString();
}

async function postForm(url, values, fetchImpl) {
  const response = await fetchImpl(url, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
      "User-Agent": "github-actions-quota",
    },
    body: formBody(values),
  });
  if (!response.ok) {
    throw new Error(`GitHub OAuth endpoint returned HTTP ${response.status}`);
  }
  return response.json();
}

export async function requestDeviceCode(clientId, fetchImpl = fetch) {
  const payload = await postForm(
    "https://github.com/login/device/code",
    { client_id: clientId },
    fetchImpl,
  );
  for (const key of ["device_code", "user_code", "verification_uri", "expires_in"]) {
    if (!payload[key]) {
      throw new Error(`GitHub device flow response is missing ${key}`);
    }
  }
  return payload;
}

export async function pollForUserToken(
  clientId,
  device,
  { fetchImpl = fetch, sleep = delay } = {},
) {
  let intervalSeconds = Number(device.interval || 5);
  const deadline = Date.now() + Number(device.expires_in) * 1000;

  while (Date.now() < deadline) {
    await sleep(intervalSeconds * 1000);
    const payload = await postForm(
      "https://github.com/login/oauth/access_token",
      {
        client_id: clientId,
        device_code: device.device_code,
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
      },
      fetchImpl,
    );

    if (payload.access_token) {
      if (payload.expires_in || payload.refresh_token) {
        throw new Error(
          "The GitHub App issued an expiring user token. Disable user-token expiration for the app before using repository-secret mode.",
        );
      }
      return payload.access_token;
    }

    if (payload.error === "authorization_pending") continue;
    if (payload.error === "slow_down") {
      intervalSeconds += Number(payload.interval || 5);
      continue;
    }
    if (payload.error) {
      throw new Error(payload.error_description || payload.error);
    }
    throw new Error("Unexpected GitHub device flow response");
  }

  throw new Error("GitHub device authorization expired before it was approved");
}

export function openBrowser(url) {
  const commands =
    process.platform === "darwin"
      ? [["open", [url]]]
      : process.platform === "win32"
        ? [["cmd", ["/c", "start", "", url]]]
        : [["xdg-open", [url]]];

  try {
    const [command, args] = commands[0];
    const child = spawn(command, args, {
      detached: true,
      stdio: "ignore",
    });
    child.unref();
    return true;
  } catch {
    return false;
  }
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
