import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {basename, dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {assetNames, compareVersions, hash, isNewestInMajor, nextVersion, parseChecksums, publishRelease, sharedVersion, shouldMarkLatest, updateMajorReferences, verifyAssets} from './release.mjs';

const controller = fileURLToPath(new URL('./release.mjs', import.meta.url));
const sha = 'a'.repeat(40), baseSha = 'b'.repeat(40), oldSha = 'c'.repeat(40);
function temporary(t) {
  const root = mkdtempSync(join(tmpdir(), 'quota-release-'));
  t.after(() => rmSync(root, {recursive: true, force: true})); return root;
}
function write(root, path, value) {
  mkdirSync(dirname(join(root, path)), {recursive: true}); writeFileSync(join(root, path), value);
}
function assets(t, tag = 'v1.2.4') {
  const directory = temporary(t);
  const names = assetNames(tag);
  for (const name of names) write(directory, name, Buffer.from(`executable ${name}`));
  write(directory, 'checksums.txt', names.map(name => `${hash(readFileSync(join(directory, name)))}  ${name}\n`).join(''));
  return directory;
}
function fixture(t, tag = 'v1.2.4') {
  const directory = assets(t, tag), events = [], tags = new Map(), versions = new Map([[sha, tag.slice(1)], [oldSha, '1.2.3']]);
  const releases = [], options = {tag, sha, baseSha, resume: false, directory};
  let main = baseSha, fetched = '', localTag = '', nextId = 1;
  const controls = {uploadFailure: 0, majorFailure: false, changedMain: false, publishFailure: false, leaseFailure: false};
  const runGit = args => {
    events.push(['git', ...args]);
    if (args[0] === 'status') return '';
    if (args[0] === 'config') return '';
    if (args[0] === 'fetch') { const ref = args.at(-1).split(':')[0]; if (ref.startsWith('refs/tags/')) fetched = tags.get(ref.slice(10)) || ''; return ''; }
    if (args[0] === 'rev-parse') {
      if (args[1] === 'HEAD') return sha;
      if (args[1] === 'origin/main') return controls.changedMain ? oldSha : main;
      if (args[1] === 'FETCH_HEAD^{commit}') return fetched;
      return tags.get(args[1].replace(/\^\{commit\}$/, '')) || '';
    }
    if (args[0] === 'ls-remote') { const name = args.at(-1).slice(10); return tags.has(name) ? `${tags.get(name)}\trefs/tags/${name}` : ''; }
    if (args[0] === 'show') return JSON.stringify({version: versions.get(args[1].split(':')[0])});
    if (args[0] === 'tag') { localTag = args[2]; return ''; }
    if (args[0] === 'push') {
      if (args[1] === '--atomic') { main = sha; tags.set(localTag, sha); return ''; }
      if (controls.majorFailure) throw new Error('interrupted major-tag push');
      if (controls.leaseFailure) throw new Error('major tag changed concurrently');
      const major = args.at(-1).split('refs/tags/')[1];
      assert.equal(args[1], `--force-with-lease=refs/tags/${major}:${tags.get(major) || ''}`);
      tags.set(major, sha); return '';
    }
    throw new Error(`Unexpected Git operation: ${args}`);
  };
  const client = {
    api(method, path, body) {
      events.push(['api', method, path, body]);
      if (method === 'GET' && path.startsWith('releases/tags/')) return releases.find(release => release.tag_name === path.slice(14)) || null;
      if (method === 'GET' && path === 'releases/latest') return releases.find(release => release.latest) || null;
      if (method === 'GET' && path.startsWith('releases?')) return releases;
      const assetMatch = /^releases\/(\d+)\/assets\?/.exec(path);
      if (method === 'GET' && assetMatch) return releases.find(release => release.id === Number(assetMatch[1])).assets;
      if (method === 'GET') return releases.find(release => release.id === Number(path.split('/')[1]));
      if (method === 'POST') {
        const release = {id: nextId++, ...body, assets: []}; releases.push(release); return release;
      }
      if (method === 'DELETE') {
        for (const release of releases) release.assets = release.assets.filter(asset => asset.id !== Number(path.split('/').at(-1)));
        return null;
      }
      if (method === 'PATCH') {
        if (controls.publishFailure) throw new Error('interrupted publication');
        const release = releases.find(release => release.id === Number(path.split('/')[1]));
        Object.assign(release, body); if (body.make_latest === 'true') { for (const other of releases) other.latest = false; release.latest = true; }
        return release;
      }
      throw new Error(`Unexpected API operation: ${method} ${path}`);
    },
    upload(tag, paths) {
      events.push(['upload', ...paths]);
      if (controls.uploadFailure && --controls.uploadFailure === 0) throw new Error('interrupted upload');
      const release = releases.find(release => release.tag_name === tag);
      assert.ok(release.draft, 'published assets must never be uploaded');
      for (const path of paths) release.assets.push({id: nextId++, name: basename(path), state: 'uploaded', bytes: readFileSync(path)});
    },
    download: asset => asset.bytes,
  };
  function publish(overrides = {}) {
    const previous = console.log, summary = process.env.GITHUB_STEP_SUMMARY; console.log = () => {}; delete process.env.GITHUB_STEP_SUMMARY;
    try { return publishRelease({...options, ...overrides}, {runGit, client}); } finally {
      console.log = previous; if (summary !== undefined) process.env.GITHUB_STEP_SUMMARY = summary;
    }
  }
  return {directory, events, tags, releases, controls, publish, versions, client, get main() { return main; }};
}

test('stable version increments and numeric ordering', () => {
  assert.equal(nextVersion('1.2.3', 'patch'), '1.2.4');
  assert.equal(nextVersion('1.2.3', 'minor'), '1.3.0');
  assert.equal(nextVersion('1.2.3', 'major'), '2.0.0');
  assert.equal(compareVersions('1.10.0', '1.9.9'), 1);
  for (const invalid of ['1.2.3-rc.1', 'v1.2.3', '01.2.3', '1.2', '9007199254740992.0.0']) assert.throws(() => nextVersion(invalid, 'patch'));
  assert.throws(() => nextVersion('1.2.3', 'auto'));
});
test('version and helper major references have one source of truth', t => {
  const root = temporary(t);
  write(root, 'package.json', JSON.stringify({version: '1.2.3'}));
  write(root, 'package-lock.json', JSON.stringify({version: '1.2.3', packages: {'': {version: '1.2.3'}}}));
  assert.equal(sharedVersion(root), '1.2.3');
  write(root, 'internal/setup/workflow.go', 'const actionMajor = "v1"\n');
  write(root, 'README.md', 'Current action major: `v1`\nuses: philippwallrafen/gh-actions-quota@v1\nOrganization billing unsupported in v1.\n');
  updateMajorReferences(root, 2);
  assert.match(readFileSync(join(root, 'internal/setup/workflow.go'), 'utf8'), /"v2"/);
  const readme = readFileSync(join(root, 'README.md'), 'utf8');
  assert.match(readme, /major: `v2`/); assert.match(readme, /quota@v2/); assert.match(readme, /unsupported in v1/);
  write(root, 'package-lock.json', JSON.stringify({version: '1.2.4', packages: {'': {version: '1.2.3'}}}));
  assert.throws(() => sharedVersion(root), /versions differ/);
});
test('exact platform assets and checksums exclude stale files', t => {
  const root = assets(t); write(root, 'stale.exe', 'old binary');
  assert.equal(assetNames('v2.0.0').length, 5);
  assert.equal(assetNames('v2.0.0').at(-1), 'gh-actions-quota_v2.0.0_windows-amd64.exe');
  assert.throws(() => assetNames('v1.2.3-rc.1'));
  verifyAssets('v1.2.4', name => readFileSync(join(root, name)));
  const checksums = readFileSync(join(root, 'checksums.txt'), 'utf8');
  assert.throws(() => parseChecksums(checksums + checksums.split('\n')[0], 'v1.2.4'));
  assert.throws(() => parseChecksums(checksums.replace(assetNames('v1.2.4')[0], '../stale.exe'), 'v1.2.4'));
  write(root, assetNames('v1.2.4')[0], 'corrupted');
  assert.throws(() => verifyAssets('v1.2.4', name => readFileSync(join(root, name))), /Checksum mismatch/);
});
test('new release atomically pushes before drafting and publishes before moving major tag', t => {
  const f = fixture(t); f.publish();
  assert.equal(f.main, sha); assert.equal(f.tags.get('v1.2.4'), sha); assert.equal(f.tags.get('v1'), sha);
  assert.equal(f.releases[0].draft, false); assert.equal(f.releases[0].assets.length, 6);
  const push = f.events.findIndex(event => event[0] === 'git' && event[2] === '--atomic');
  const draft = f.events.findIndex(event => event[1] === 'POST');
  const publish = f.events.findIndex(event => event[1] === 'PATCH');
  const major = f.events.findIndex(event => event[0] === 'git' && event[2]?.startsWith('--force-with-lease'));
  assert.ok(push < draft && draft < publish && publish < major);
  assert.equal(f.releases[0].make_latest, 'true');
});
test('changed main, existing tag, and corrupt artifacts fail before remote mutations', t => {
  for (const scenario of ['main', 'tag', 'checksum']) {
    const f = fixture(t);
    if (scenario === 'main') f.controls.changedMain = true;
    if (scenario === 'tag') f.tags.set('v1.2.4', oldSha);
    if (scenario === 'checksum') write(f.directory, assetNames('v1.2.4')[0], 'bad');
    assert.throws(() => f.publish());
    assert.equal(f.releases.length, 0);
    assert.ok(!f.events.some(event => event[0] === 'git' && event[1] === 'push'));
  }
});
test('interrupted draft upload resumes the existing tag without updating main', t => {
  const f = fixture(t); f.controls.uploadFailure = 3;
  assert.throws(() => f.publish(), /interrupted upload/);
  assert.equal(f.releases[0].draft, true); assert.equal(f.releases[0].assets.length, 2);
  const uploaded = f.releases[0].assets.map(asset => asset.id);
  f.events.length = 0; f.controls.uploadFailure = 0; f.publish({resume: true});
  assert.equal(f.releases[0].draft, false);
  assert.ok(uploaded.every(id => f.releases[0].assets.some(asset => asset.id === id)));
  assert.ok(!f.events.some(event => event[0] === 'git' && event[2] === '--atomic'));
});
test('published recovery leaves all assets fixed and finishes a missing major tag', t => {
  const f = fixture(t); f.controls.majorFailure = true;
  assert.throws(() => f.publish(), /interrupted major/);
  assert.equal(f.releases[0].draft, false);
  const original = f.releases[0].assets.map(asset => hash(asset.bytes));
  // A newer local build is allowed to differ from already-published assets.
  for (const name of assetNames('v1.2.4')) write(f.directory, name, 'new toolchain build');
  write(f.directory, 'checksums.txt', assetNames('v1.2.4').map(name => `${hash(readFileSync(join(f.directory, name)))}  ${name}\n`).join(''));
  f.events.length = 0; f.controls.majorFailure = false; f.publish({resume: true});
  assert.deepEqual(f.releases[0].assets.map(asset => hash(asset.bytes)), original);
  assert.ok(!f.events.some(event => event[0] === 'upload' || (event[0] === 'api' && ['POST', 'PATCH', 'DELETE'].includes(event[1]))));
  assert.equal(f.tags.get('v1'), sha);
});
test('stale draft assets can be replaced, but published assets cannot', t => {
  const f = fixture(t); f.controls.publishFailure = true;
  assert.throws(() => f.publish(), /interrupted publication/);
  f.releases[0].assets[0].bytes = Buffer.from('broken partial draft');
  f.events.length = 0; f.controls.publishFailure = false; f.publish({resume: true});
  assert.ok(f.events.some(event => event[1] === 'DELETE'));
  assert.equal(f.releases[0].draft, false);
});
test('older recovery cannot regress latest or the major tag', t => {
  const f = fixture(t);
  f.releases.push({id: 999, tag_name: 'v1.10.0', draft: false, prerelease: false, latest: true, assets: []});
  f.tags.set('v1', oldSha); f.versions.set(oldSha, '1.10.0');
  const result = f.publish();
  assert.equal(f.releases.find(release => release.tag_name === 'v1.2.4').make_latest, 'false');
  assert.equal(f.tags.get('v1'), oldSha); assert.match(result.majorResult, /unchanged/);
  assert.equal(shouldMarkLatest('v1.2.4', {tag_name: 'custom-latest'}), false);
  assert.equal(isNewestInMajor('v2.0.0', f.releases), true);
});
test('future major pointer and concurrent tag updates cannot be overwritten', t => {
  const f = fixture(t); f.tags.set('v1', oldSha); f.versions.set(oldSha, '1.9.0');
  f.publish(); assert.equal(f.tags.get('v1'), oldSha);
  const race = fixture(t); race.controls.leaseFailure = true;
  assert.throws(() => race.publish(), /changed concurrently/);
  assert.equal(race.releases[0].draft, false);
});

function gitAt(root, ...args) { return execFileSync('git', args, {cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe']}).trim(); }
function preparationRepo(t) {
  const root = temporary(t), origin = join(root, 'origin.git'), checkout = join(root, 'checkout');
  mkdirSync(checkout); gitAt(root, 'init', '--bare', origin); gitAt(checkout, 'init', '-b', 'main');
  gitAt(checkout, 'config', 'user.name', 'test'); gitAt(checkout, 'config', 'user.email', 'test@example.invalid');
  write(checkout, 'package.json', JSON.stringify({name: 'release-fixture', version: '1.2.3', private: true}, null, 2) + '\n');
  write(checkout, 'package-lock.json', JSON.stringify({name: 'release-fixture', version: '1.2.3', lockfileVersion: 3, packages: {'': {name: 'release-fixture', version: '1.2.3'}}}, null, 2) + '\n');
  write(checkout, 'internal/setup/workflow.go', 'const actionMajor = "v1"\n');
  write(checkout, 'README.md', 'Current action major: `v1`\n');
  gitAt(checkout, 'add', '.'); gitAt(checkout, 'commit', '-m', 'initial'); gitAt(checkout, 'remote', 'add', 'origin', origin); gitAt(checkout, 'push', '-u', 'origin', 'main');
  return {root, origin, checkout};
}
function prepare(repo, bump, resumeTag = '', bundle = join(repo.root, 'candidate.bundle')) {
  return execFileSync(process.execPath, [controller, 'prepare'], {cwd: repo.checkout, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], env: {...process.env, GITHUB_OUTPUT: '', GITHUB_REF_NAME: 'main', BUMP: bump, RESUME_TAG: resumeTag, CANDIDATE_BUNDLE: bundle}});
}
test('candidate preparation commits a synchronized version and exports the exact Git bundle', t => {
  const repo = preparationRepo(t), base = gitAt(repo.checkout, 'rev-parse', 'HEAD');
  const output = prepare(repo, 'major');
  assert.match(output, /tag: v2\.0\.0/); assert.equal(sharedVersion(repo.checkout), '2.0.0');
  assert.match(readFileSync(join(repo.checkout, 'internal/setup/workflow.go'), 'utf8'), /"v2"/);
  assert.equal(gitAt(repo.checkout, 'rev-parse', 'HEAD^'), base);
  assert.equal(gitAt(repo.origin, 'rev-parse', 'refs/heads/main'), base, 'preparation must not push');
  const fresh = join(repo.root, 'fresh'); mkdirSync(fresh); gitAt(fresh, 'init');
  const sha = gitAt(repo.checkout, 'rev-parse', 'HEAD');
  execFileSync(process.execPath, [controller, 'restore', join(repo.root, 'candidate.bundle'), sha], {cwd: fresh, stdio: 'pipe'});
  assert.equal(gitAt(fresh, 'rev-parse', 'HEAD'), sha); assert.equal(sharedVersion(fresh), '2.0.0');
  assert.throws(() => execFileSync(process.execPath, [controller, 'restore', join(repo.root, 'candidate.bundle'), base], {cwd: fresh, stdio: 'pipe'}));
});
test('preparation refuses existing tags and recovery uses their commit without bumping main', t => {
  const repo = preparationRepo(t); prepare(repo, 'patch');
  const candidate = gitAt(repo.checkout, 'rev-parse', 'HEAD');
  gitAt(repo.checkout, 'tag', 'v1.2.4'); gitAt(repo.checkout, 'push', '--atomic', 'origin', 'main', 'v1.2.4');
  write(repo.checkout, 'README.md', 'development after release\n'); gitAt(repo.checkout, 'add', 'README.md'); gitAt(repo.checkout, 'commit', '-m', 'development'); gitAt(repo.checkout, 'push', 'origin', 'main');
  const main = gitAt(repo.origin, 'rev-parse', 'refs/heads/main');
  assert.match(prepare(repo, 'major', 'v1.2.4', join(repo.root, 'resume.bundle')), /resume: true/);
  assert.equal(gitAt(repo.checkout, 'rev-parse', 'HEAD'), candidate); assert.equal(sharedVersion(repo.checkout), '1.2.4');
  assert.equal(gitAt(repo.origin, 'rev-parse', 'refs/heads/main'), main);
  // A tag for the next version prevents a fresh preparation.
  gitAt(repo.checkout, 'checkout', 'main'); gitAt(repo.checkout, 'tag', 'v1.2.5'); gitAt(repo.checkout, 'push', 'origin', 'v1.2.5');
  assert.throws(() => prepare(repo, 'patch'), /already exists/);
});

test('checked-in package, helper and documented action major agree', () => {
  const root = dirname(dirname(controller)), major = sharedVersion(root).split('.')[0];
  assert.match(readFileSync(join(root, 'internal/setup/workflow.go'), 'utf8'), new RegExp(`const actionMajor = "v${major}"`));
  assert.ok(readFileSync(join(root, 'README.md'), 'utf8').includes(`Current action major: \`v${major}\``));
});
test('real Git publication pushes commit and version tag atomically and updates an annotated major tag', t => {
  const repo = preparationRepo(t), base = gitAt(repo.checkout, 'rev-parse', 'HEAD');
  gitAt(repo.checkout, 'tag', '-a', 'v1', '-m', 'compatibility tag'); gitAt(repo.checkout, 'push', 'origin', 'v1');
  prepare(repo, 'patch');
  const candidate = gitAt(repo.checkout, 'rev-parse', 'HEAD');
  const f = fixture(t), previous = console.log, summary = process.env.GITHUB_STEP_SUMMARY;
  console.log = () => {}; delete process.env.GITHUB_STEP_SUMMARY;
  try {
    publishRelease({tag: 'v1.2.4', sha: candidate, baseSha: base, resume: false, directory: f.directory}, {runGit: args => gitAt(repo.checkout, ...args), client: f.client});
  } finally { console.log = previous; if (summary !== undefined) process.env.GITHUB_STEP_SUMMARY = summary; }
  assert.equal(gitAt(repo.origin, 'rev-parse', 'refs/heads/main'), candidate);
  assert.equal(gitAt(repo.origin, 'rev-parse', 'v1.2.4^{commit}'), candidate);
  assert.equal(gitAt(repo.origin, 'rev-parse', 'v1^{commit}'), candidate);
});
