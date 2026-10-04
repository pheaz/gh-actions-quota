import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import test from 'node:test';
import { executeBinary, launch, platformTarget, releaseAsset, verifyChecksum } from './launcher.js';

const binary = Buffer.from('test binary');
const checksum = createHash('sha256').update(binary).digest('hex');

for (const [platform, arch, target] of [
  ['darwin', 'arm64', 'darwin-arm64'], ['darwin', 'x64', 'darwin-amd64'],
  ['linux', 'arm64', 'linux-arm64'], ['linux', 'x64', 'linux-amd64'],
  ['win32', 'x64', 'windows-amd64.exe'],
]) {
  test(`maps ${platform}/${arch} to an exact release asset`, () => {
    assert.equal(platformTarget(platform, arch), target);
    const name = `gh-actions-quota_v1.2.3_${target}`;
    assert.deepEqual(releaseAsset('1.2.3', platform, arch), {
      name,
      binaryURL: `https://github.com/philippwallrafen/gh-actions-quota/releases/download/v1.2.3/${name}`,
      checksumsURL: 'https://github.com/philippwallrafen/gh-actions-quota/releases/download/v1.2.3/checksums.txt',
    });
  });
}

test('unsupported platforms and unpinned versions fail safely', () => {
  for (const [platform, arch] of [['win32', 'arm64'], ['linux', 'ia32'], ['freebsd', 'x64'], ['secret', 'secret']]) {
    assert.throws(() => platformTarget(platform, arch), { message: 'Unsupported runner platform or architecture' });
  }
  for (const version of ['latest', 'v1.2.3', '1', '1.2.3-rc.1', '01.2.3', '', null]) {
    assert.throws(() => releaseAsset(version, 'linux', 'x64'), { message: 'Invalid exact release version' });
  }
});

test('valid SHA256 manifests match, including CRLF and binary markers', () => {
  verifyChecksum(binary, `${checksum}  binary\n`, 'binary');
  verifyChecksum(binary, `${checksum.toUpperCase()} *binary\r\n`, 'binary');
});

test('mismatches, missing assets, malformed and duplicate entries fail', () => {
  assert.throws(() => verifyChecksum(Buffer.from('corrupt'), `${checksum}  binary\n`, 'binary'), /checksum mismatch/);
  assert.throws(() => verifyChecksum(binary, `${checksum}  other\n`, 'binary'), /missing/);
  assert.throws(() => verifyChecksum(binary, 'invalid', 'binary'), /Invalid/);
  assert.throws(() => verifyChecksum(binary, `${checksum}  binary\n${checksum}  binary\n`, 'binary'), /Invalid/);
});

test('executes action with inherited environment/stdio and propagates child exit codes', async () => {
  const env = { TEST_TOKEN: 'never-print-this' };
  for (const code of [0, 1, 17, 255, null]) {
    const exit = await executeBinary('/verified/binary', {
      env,
      spawnImpl(path, args, options) {
        assert.equal(path, '/verified/binary');
        assert.deepEqual(args, ['action']);
        assert.deepEqual(options, { env, stdio: 'inherit' });
        const child = new EventEmitter();
        queueMicrotask(() => child.emit('exit', code));
        return child;
      },
    });
    assert.equal(exit, code ?? 1);
  }
});

test('child startup errors are sanitized', async () => {
  await assert.rejects(executeBinary('binary', {
    spawnImpl() {
      const child = new EventEmitter();
      queueMicrotask(() => child.emit('error', new Error('secret transport dump')));
      return child;
    },
  }), { message: 'Could not start the verified release binary' });
});

async function packageFixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'launcher-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const path = join(directory, 'package.json');
  await writeFile(path, JSON.stringify({ version: '1.2.3' }));
  return path;
}

test('downloads both pinned assets, verifies before execution, chmods and cleans up', async t => {
  const packageURL = await packageFixture(t);
  const asset = releaseAsset('1.2.3', 'linux', 'x64'), requests = [];
  let binaryPath;
  const code = await launch({
    packageURL, platform: 'linux', arch: 'x64',
    async downloadImpl(url) {
      requests.push(url);
      return url === asset.binaryURL ? binary : Buffer.from(`${checksum}  ${asset.name}\n`);
    },
    async executeImpl(path) {
      binaryPath = path;
      assert.equal(basename(path), asset.name);
      assert.deepEqual(await readFile(path), binary);
      if (process.platform !== 'win32') assert.equal((await stat(path)).mode & 0o777, 0o700);
      return 23;
    },
  });
  assert.equal(code, 23);
  assert.deepEqual(requests, [asset.binaryURL, asset.checksumsURL]);
  await assert.rejects(stat(binaryPath), { code: 'ENOENT' });
});

test('download and checksum failures never execute a binary', async t => {
  const packageURL = await packageFixture(t);
  const asset = releaseAsset('1.2.3', 'linux', 'x64');
  for (const failure of ['binary download', 'manifest download', 'checksum']) {
    let executions = 0;
    await assert.rejects(launch({
      packageURL, platform: 'linux', arch: 'x64',
      async downloadImpl(url) {
        if (failure === 'binary download' || (failure === 'manifest download' && url === asset.checksumsURL)) throw new Error('download failed');
        return url === asset.binaryURL ? binary : Buffer.from(`${'0'.repeat(64)}  ${asset.name}\n`);
      },
      async executeImpl() { executions++; return 0; },
    }));
    assert.equal(executions, 0);
  }
});
