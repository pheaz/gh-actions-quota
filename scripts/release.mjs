#!/usr/bin/env node
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {appendFileSync, mkdirSync, readFileSync, writeFileSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

export const candidateRef = 'refs/heads/codex/release-candidate';
export const targets = ['darwin-arm64', 'darwin-amd64', 'linux-amd64', 'linux-arm64', 'windows-amd64'];

export function versionParts(version) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) throw new Error(`Expected a stable semantic version, got ${version}`);
  const parts = version.split('.').map(Number);
  if (!parts.every(Number.isSafeInteger)) throw new Error('Version components exceed the supported integer range');
  return parts;
}
export function tagVersion(tag) {
  if (!tag.startsWith('v')) throw new Error('Release tags must start with v');
  const version = tag.slice(1); versionParts(version); return version;
}
export function compareVersions(a, b) {
  const left = versionParts(a), right = versionParts(b);
  for (let i = 0; i < 3; i++) if (left[i] !== right[i]) return left[i] > right[i] ? 1 : -1;
  return 0;
}
export function nextVersion(version, bump) {
  const parts = versionParts(version), index = ['major', 'minor', 'patch'].indexOf(bump);
  if (index < 0) throw new Error(`Unsupported version increment: ${bump}`);
  parts[index]++; for (let i = index + 1; i < 3; i++) parts[i] = 0;
  const next = parts.join('.'); versionParts(next); return next;
}
export function assetNames(tag) {
  tagVersion(tag);
  return targets.map(target => `gh-actions-quota_${tag}_${target}${target.startsWith('windows-') ? '.exe' : ''}`);
}
export function hash(data) { return createHash('sha256').update(data).digest('hex'); }
export function parseChecksums(text, tag) {
  const expected = assetNames(tag), hashes = new Map();
  for (const line of text.trim().split(/\r?\n/)) {
    const match = /^([a-f0-9]{64}) [ *](\S+)$/.exec(line);
    if (!match || !expected.includes(match[2]) || hashes.has(match[2])) throw new Error('Invalid checksum manifest or unexpected asset');
    hashes.set(match[2], match[1]);
  }
  if (hashes.size !== expected.length) throw new Error('Checksum manifest must contain exactly five platform executables');
  return hashes;
}
export function verifyAssets(tag, read) {
  const hashes = parseChecksums(read('checksums.txt').toString(), tag);
  for (const [name, checksum] of hashes) if (hash(read(name)) !== checksum) throw new Error(`Checksum mismatch: ${name}`);
}
export function sharedVersion(root = '.') {
  const pkg = JSON.parse(readFileSync(resolve(root, 'package.json')));
  const lock = JSON.parse(readFileSync(resolve(root, 'package-lock.json')));
  versionParts(pkg.version);
  if (lock.version !== pkg.version || lock.packages?.['']?.version !== pkg.version) throw new Error('Package and lockfile versions differ');
  return pkg.version;
}
export function updateMajorReferences(root, major) {
  const source = resolve(root, 'internal/setup/workflow.go');
  const text = readFileSync(source, 'utf8');
  const matches = text.match(/^const actionMajor = "v\d+"$/gm);
  if (matches?.length !== 1) throw new Error('Expected exactly one generated-helper major-version constant');
  writeFileSync(source, text.replace(/^const actionMajor = "v\d+"$/m, `const actionMajor = "v${major}"`));
  const readme = resolve(root, 'README.md');
  writeFileSync(readme, readFileSync(readme, 'utf8').replace(/(philippwallrafen\/gh-actions-quota@|Current action major: `)v\d+/g, `$1v${major}`));
}

function command(program, args, options = {}) {
  return execFileSync(program, args, {encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], maxBuffer: 64 * 1024 * 1024, ...options}).trim();
}
function git(args) { return command('git', args); }
function output(values) {
  for (const [key, value] of Object.entries(values)) {
    console.log(`${key}: ${value}`);
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
}
function requireClean() {
  if (git(['status', '--porcelain']) !== '') throw new Error('Release preparation requires a clean checkout');
}
export function restoreCandidate(bundle, sha) {
  if (!/^[a-f0-9]{40}$/.test(sha)) throw new Error('Invalid candidate commit SHA');
  git(['bundle', 'verify', bundle]);
  git(['fetch', '--no-tags', bundle, candidateRef]);
  if (git(['rev-parse', 'FETCH_HEAD^{commit}']) !== sha) throw new Error('Candidate bundle does not match its expected commit');
  git(['checkout', '--detach', sha]);
  if (git(['rev-parse', 'HEAD']) !== sha) throw new Error('Candidate checkout failed');
}
export function prepareCandidate({bump, resumeTag, bundle}) {
  requireClean();
  if (process.env.GITHUB_REF_NAME !== 'main') throw new Error('Run the Release workflow from main');
  const baseSha = git(['rev-parse', 'HEAD']);
  git(['fetch', '--tags', 'origin', 'main']);
  let tag;
  if (resumeTag) {
    tagVersion(resumeTag); tag = resumeTag;
    const remote = git(['ls-remote', 'origin', `refs/tags/${tag}`]);
    if (!remote) throw new Error(`Cannot resume: remote tag ${tag} does not exist`);
    git(['fetch', '--force', 'origin', `refs/tags/${tag}:refs/tags/${tag}`]);
    git(['checkout', '--detach', `${tag}^{commit}`]);
    if (`v${sharedVersion()}` !== tag) throw new Error('Tagged package version does not match resume_tag');
  } else {
    if (git(['rev-parse', 'origin/main']) !== baseSha) throw new Error('main changed before preparation; start a new run or use resume_tag');
    const current = sharedVersion(), next = nextVersion(current, bump); tag = `v${next}`;
    if (git(['ls-remote', 'origin', `refs/tags/${tag}`])) throw new Error(`Tag ${tag} already exists; use resume_tag to recover it`);
    command('npm', ['version', next, '--no-git-tag-version', '--ignore-scripts']);
    updateMajorReferences('.', versionParts(next)[0]);
    if (sharedVersion() !== next) throw new Error('Version update failed');
    const allowed = ['package.json', 'package-lock.json', 'internal/setup/workflow.go', 'README.md'];
    const changed = git(['diff', '--name-only']).split('\n').filter(Boolean);
    if (changed.some(path => !allowed.includes(path))) throw new Error('Unexpected files changed during release preparation');
    git(['diff', '--check']);
    git(['config', 'user.name', 'github-actions[bot]']);
    git(['config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com']);
    git(['add', ...allowed]);
    git(['commit', '-m', `release: ${tag}`]);
  }
  requireClean();
  const sha = git(['rev-parse', 'HEAD']);
  mkdirSync(dirname(bundle), {recursive: true});
  git(['update-ref', candidateRef, sha]);
  git(['bundle', 'create', bundle, candidateRef]);
  return {tag, sha, base_sha: baseSha, resume: String(Boolean(resumeTag))};
}

function githubClient(repo) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo)) throw new Error('Invalid GitHub repository');
  const prefix = `repos/${repo}`;
  const api = (method, path, body, missing = false) => {
    const args = ['api', '--method', method, `${prefix}/${path}`, '-H', 'X-GitHub-Api-Version: 2026-03-10'];
    if (body !== undefined) args.push('--input', '-');
    try {
      const result = command('gh', args, body === undefined ? {} : {input: JSON.stringify(body)});
      return result ? JSON.parse(result) : null;
    } catch (error) {
      if (missing && /HTTP 404\b/.test(error.stderr?.toString() ?? '')) return null;
      throw error;
    }
  };
  return {
    api,
    upload: (tag, paths) => command('gh', ['release', 'upload', tag, ...paths, '--repo', repo]),
    download: asset => execFileSync('gh', ['api', `${prefix}/releases/assets/${asset.id}`, '-H', 'Accept: application/octet-stream'], {maxBuffer: 64 * 1024 * 1024}),
  };
}
function stableReleases(releases) {
  return releases.filter(release => !release.draft && !release.prerelease && /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(release.tag_name));
}
export function shouldMarkLatest(tag, latest) {
  if (!latest) return true;
  // An unrecognized existing latest release is not silently displaced.
  try { return compareVersions(tagVersion(tag), tagVersion(latest.tag_name)) > 0; } catch { return false; }
}
export function isNewestInMajor(tag, releases) {
  const version = tagVersion(tag), major = versionParts(version)[0];
  return !stableReleases(releases).some(release => versionParts(tagVersion(release.tag_name))[0] === major && compareVersions(tagVersion(release.tag_name), version) > 0);
}
function listReleases(client) {
  const releases = [];
  for (let page = 1; ; page++) {
    const batch = client.api('GET', `releases?per_page=100&page=${page}`);
    releases.push(...batch);
    if (batch.length < 100) return releases;
  }
}
function releaseAssets(client, release) {
  const assets = [];
  for (let page = 1; ; page++) {
    const batch = client.api('GET', `releases/${release.id}/assets?per_page=100&page=${page}`);
    assets.push(...batch);
    if (batch.length < 100) return assets;
  }
}
function assertAssetSet(assets, tag) {
  const names = [...assetNames(tag), 'checksums.txt'];
  if (assets.length !== names.length || new Set(assets.map(asset => asset.name)).size !== names.length || assets.some(asset => !names.includes(asset.name) || asset.state !== 'uploaded')) throw new Error('Release must contain exactly five uploaded executables and checksums.txt');
}
function verifyRemoteAssets(client, release, tag) {
  const assets = releaseAssets(client, release); assertAssetSet(assets, tag);
  const byName = new Map(assets.map(asset => [asset.name, asset]));
  verifyAssets(tag, name => client.download(byName.get(name)));
}
function assertRemoteTag(runGit, tag, sha) {
  runGit(['fetch', '--force', 'origin', `refs/tags/${tag}:refs/tags/${tag}`]);
  if (runGit(['rev-parse', `${tag}^{commit}`]) !== sha) throw new Error('Remote version tag does not match the validated candidate');
}

// External mutations are deliberately confined to this boundary. The injected
// Git/GitHub operations let tests exercise interruptions without publishing.
export function publishRelease({tag, sha, baseSha, resume, directory}, {runGit = git, client = githubClient(process.env.GITHUB_REPOSITORY)} = {}) {
  const version = tagVersion(tag);
  if (!/^[a-f0-9]{40}$/.test(sha) || !/^[a-f0-9]{40}$/.test(baseSha)) throw new Error('Invalid release commit SHA');
  if (runGit(['rev-parse', 'HEAD']) !== sha) throw new Error('Publication checkout differs from validated candidate');
  if (runGit(['status', '--porcelain']) !== '') throw new Error('Publication requires a clean candidate checkout');
  const read = name => readFileSync(resolve(directory, name));
  verifyAssets(tag, read);
  let release = client.api('GET', `releases/tags/${tag}`, undefined, true);
  if (!resume) {
    if (release) throw new Error('Release already exists; use resume_tag');
    runGit(['fetch', 'origin', 'main']);
    if (runGit(['rev-parse', 'origin/main']) !== baseSha) throw new Error('main changed while the release was running; refusing to publish');
    if (runGit(['ls-remote', 'origin', `refs/tags/${tag}`])) throw new Error('Version tag appeared during validation; refusing to overwrite it');
    runGit(['config', 'user.name', 'github-actions[bot]']);
    runGit(['config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com']);
    runGit(['tag', '-a', tag, sha, '-m', `gh-actions-quota ${tag}`]);
    runGit(['push', '--atomic', 'origin', `${sha}:refs/heads/main`, `refs/tags/${tag}`]);
  }
  assertRemoteTag(runGit, tag, sha);
  if (!release) release = client.api('POST', 'releases', {tag_name: tag, target_commitish: 'main', name: tag, draft: true, prerelease: false, generate_release_notes: true, make_latest: 'false'});
  if (release.prerelease) throw new Error('Cannot resume a prerelease using the stable release workflow');
  if (release.draft) {
    let assets = releaseAssets(client, release);
    const expected = [...assetNames(tag), 'checksums.txt'];
    if (assets.some(asset => !expected.includes(asset.name))) throw new Error('Draft contains unexpected assets; review them before resuming');
    for (const name of expected) {
      const matches = assets.filter(asset => asset.name === name);
      if (matches.length > 1) throw new Error('Draft contains duplicate assets');
      const existing = matches[0];
      if (existing && existing.state === 'uploaded' && hash(client.download(existing)) === hash(read(name))) continue;
      // Refresh immediately before touching a draft. Published assets are never
      // deleted or overwritten, including on recovery runs.
      const current = client.api('GET', `releases/${release.id}`);
      if (!current.draft) throw new Error('Draft was published during upload; resume the published release instead');
      if (existing) client.api('DELETE', `releases/assets/${existing.id}`);
      client.upload(tag, [resolve(directory, name)]);
    }
    verifyRemoteAssets(client, release, tag);
    const latest = client.api('GET', 'releases/latest', undefined, true);
    release = client.api('PATCH', `releases/${release.id}`, {draft: false, make_latest: shouldMarkLatest(tag, latest) ? 'true' : 'false'});
  } else {
    // Recovery after publication verifies the published manifest itself, rather
    // than requiring an older published build to match a new toolchain build.
    verifyRemoteAssets(client, release, tag);
  }
  assertRemoteTag(runGit, tag, sha);
  const majorTag = `v${versionParts(version)[0]}`;
  let majorResult = 'unchanged (newer release exists)';
  if (isNewestInMajor(tag, listReleases(client))) {
    if (client.api('GET', `releases/tags/${majorTag}`, undefined, true)) throw new Error('A movable major tag must not have an associated GitHub Release');
    const remote = runGit(['ls-remote', 'origin', `refs/tags/${majorTag}`]);
    const oldOid = remote ? remote.split(/\s+/)[0] : '';
    let advance = true;
    if (oldOid) {
      runGit(['fetch', '--no-tags', 'origin', `refs/tags/${majorTag}`]);
      const currentSha = runGit(['rev-parse', 'FETCH_HEAD^{commit}']);
      const currentVersion = JSON.parse(runGit(['show', `${currentSha}:package.json`])).version;
      if (compareVersions(currentVersion, version) > 0) advance = false;
      if (compareVersions(currentVersion, version) === 0 && currentSha !== sha) throw new Error('Major tag points to a different commit with the same version');
      if (currentSha === sha) { advance = false; majorResult = 'already current'; }
    }
    if (advance) {
      runGit(['push', `--force-with-lease=refs/tags/${majorTag}:${oldOid}`, 'origin', `${sha}:refs/tags/${majorTag}`]);
      majorResult = `advanced to ${tag}`;
    }
  }
  const summary = `## Released ${tag}\n\nSource commit: \`${sha}\`\n\nAssets:\n${[...assetNames(tag), 'checksums.txt'].map(name => `- \`${name}\``).join('\n')}\n\nMajor tag \`${majorTag}\`: ${majorResult}\n`;
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
  console.log(summary);
  return {tag, sha, majorTag, majorResult};
}

function main() {
  const [operation, ...args] = process.argv.slice(2);
  switch (operation) {
    case 'prepare': output(prepareCandidate({bump: process.env.BUMP || 'patch', resumeTag: process.env.RESUME_TAG || '', bundle: resolve(process.env.CANDIDATE_BUNDLE)})); break;
    case 'restore': restoreCandidate(args[0], args[1]); break;
    case 'assets': console.log(assetNames(args[0]).join('\n')); break;
    case 'verify-assets': verifyAssets(args[0], name => readFileSync(resolve(args[1], name))); break;
    case 'publish': publishRelease({tag: process.env.RELEASE_TAG, sha: process.env.CANDIDATE_SHA, baseSha: process.env.BASE_SHA, resume: process.env.RESUME === 'true', directory: process.env.ASSET_DIRECTORY}); break;
    default: throw new Error('Usage: release.mjs prepare|restore|assets|verify-assets|publish');
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(); } catch (error) { console.error(error.message); if (error.stderr) console.error(error.stderr.toString()); process.exitCode = 1; }
}
