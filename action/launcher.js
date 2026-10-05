import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

class LauncherError extends Error {}

const repository = 'https://github.com/philippwallrafen/gh-actions-quota';

export function platformTarget(platform, arch) {
  const targets = {
    'darwin/arm64': 'darwin-arm64', 'darwin/x64': 'darwin-amd64',
    'linux/arm64': 'linux-arm64', 'linux/x64': 'linux-amd64',
    'win32/x64': 'windows-amd64.exe',
  };
  const target = targets[`${platform}/${arch}`];
  if (!target) throw new LauncherError('Unsupported runner platform or architecture');
  return target;
}

export function releaseAsset(version, platform, arch) {
  if (typeof version !== 'string' || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
    throw new LauncherError('Invalid exact release version');
  }
  const tag = `v${version}`;
  const name = `gh-actions-quota_${tag}_${platformTarget(platform, arch)}`;
  const base = `${repository}/releases/download/${tag}`;
  return { name, binaryURL: `${base}/${name}`, checksumsURL: `${base}/checksums.txt` };
}

export function verifyChecksum(binary, manifest, assetName) {
  const entries = manifest.trim().split(/\r?\n/).map(line => /^([a-fA-F0-9]{64}) [ *](\S+)$/.exec(line));
  if (entries.some(entry => !entry) || new Set(entries.map(entry => entry[2])).size !== entries.length) {
    throw new LauncherError('Invalid release checksum manifest');
  }
  const entry = entries.find(entry => entry[2] === assetName);
  if (!entry) throw new LauncherError('Selected binary is missing from release checksums');
  const actual = createHash('sha256').update(binary).digest('hex');
  if (actual !== entry[1].toLowerCase()) throw new LauncherError('Release binary SHA256 checksum mismatch');
}

async function download(url) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(120_000) });
    if (!response.ok) throw new LauncherError('download failed');
    return Buffer.from(await response.arrayBuffer());
  } catch {
    throw new LauncherError('Could not download the exact release asset');
  }
}

export function executeBinary(path, { spawnImpl = spawn, env = process.env } = {}) {
  return new Promise((resolveExit, reject) => {
    let child;
    try {
      child = spawnImpl(path, ['action'], { env, stdio: 'inherit' });
    } catch {
      reject(new LauncherError('Could not start the verified release binary'));
      return;
    }
    child.once('error', () => reject(new LauncherError('Could not start the verified release binary')));
    // A child terminated by a signal has no exit code and must still fail.
    child.once('exit', code => resolveExit(code ?? 1));
  });
}

// Injection keeps bootstrap tests independent of the network and published releases.
export async function launch({
  platform = process.platform, arch = process.arch,
  packageURL = new URL('../package.json', import.meta.url),
  downloadImpl = download, executeImpl = executeBinary,
} = {}) {
  let version;
  try {
    version = JSON.parse(await readFile(packageURL, 'utf8')).version;
  } catch {
    throw new LauncherError('Could not read the exact release version');
  }
  const asset = releaseAsset(version, platform, arch);
  let directory;
  try {
    const binary = await downloadImpl(asset.binaryURL);
    const manifest = await downloadImpl(asset.checksumsURL);
    verifyChecksum(binary, manifest.toString(), asset.name);
    directory = await mkdtemp(join(tmpdir(), 'gh-actions-quota-'));
    const path = join(directory, asset.name);
    await writeFile(path, binary, { mode: 0o700 });
    if (platform !== 'win32') await chmod(path, 0o700);
    return await executeImpl(path);
  } finally {
    if (directory) await rm(directory, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.exitCode = await launch();
  } catch (error) {
    // Never log transport/filesystem errors that can contain environment secrets.
    console.error(`gh-actions-quota launcher failed: ${error instanceof LauncherError ? error.message : 'Could not prepare or execute the verified release binary'}`);
    process.exitCode = 1;
  }
}
