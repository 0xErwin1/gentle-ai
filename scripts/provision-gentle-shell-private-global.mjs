// Owned, bounded stock-npm composition. No custom Pi updater or lifecycle hooks.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

const [root, prefix, agent, finalPrefix, finalRoot, mode, requestedAction = 'install'] = process.argv.slice(2);
const preparing = requestedAction === 'prepare-prior';
const action = preparing ? 'install' : requestedAction;
const uid = process.getuid();
const reject = message => { throw Error(`global provision refused: ${message}`); };
const digest = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const packageName = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;
const modernPins = {
  'gentle-pi': ['4.0.0', 'sha512-ZG/diWBSKPfjU4MjiHVUXxHWQvDSRS2dvpPCma4ZINuV+8UGdZAPHca6vcK27FLAAG7crlJpwdWOcwVNW4rh/Q=='],
  '@earendil-works/pi-coding-agent': ['1.0.0', 'sha512-/FtbxoSQU/mEv1QnichJjRjqteqaIaMWxmhB4G367+MwZfX7/DI5B9YAg5lqbN7nztFskBEtUSZ+FlmMBECtMw=='],
  '@earendil-works/pi-tui': ['1.0.0', 'sha512-JsT7kXnpZA2YOtQu6RyriyxEO0eJIzPyfiH09bH+OLN5+s18HYkwaUD/tBkjhnSfMu6/50CQPRYJagzSP6HdPw=='],
  '@heyhuynhgiabuu/pi-pretty': ['0.6.27', 'sha512-4Jj+n6ZBFdn979fWAA3nMcJ45Q5qtcLeq1Pe6+Oo2LDIpDhqv7heoTKkBpq9G74/pNYc0EaXR28pssQ5Wbc5bg=='],
  'typebox': ['1.3.27', 'sha512-zu+jc1pcy4UiNThxikUr36f0Rybk9PEeCg/NE6adeWr/SKsdNO4EzZHYRDlv2YCVAfj3Odq3dESSo/jNyoBXzA=='],
};
const priorCoding = ['0.99.2', 'sha512-6R1BZ2N77CrVcGf3eC2KovTz1Q4RYiAeydvVWQT546N2fi1nBc81aURlbOZCgruWoW9VY/UrLzDynF4YTolpoA=='];
const priorTui = 'sha512-IOcNnd390NCIwBEKeOiBK/bvaR7Z+vEsvB5TtKsFdsY/vAZE6qRHbQqt6jpTefTiKPfQlwG8eS1BU42DeG2v4w==';
let pins = modernPins;
function physical(absolute, directory = false) {
  if (!path.isAbsolute(absolute) || path.resolve(absolute) !== absolute || fs.realpathSync(absolute) !== absolute) reject('noncanonical path');
  const stat = fs.lstatSync(absolute);
  if (stat.uid !== uid || (stat.mode & 0o022) !== 0 || (directory ? !stat.isDirectory() : !stat.isFile())) reject('foreign or nonphysical object');
  return stat;
}
function read(absolute) {
  const before = physical(absolute);
  if (before.size > 33554432) reject('file byte bound');
  const bytes = fs.readFileSync(absolute);
  const after = physical(absolute);
  if (before.ino !== after.ino || before.dev !== after.dev || before.size !== after.size || before.mtimeMs !== after.mtimeMs || before.ctimeMs !== after.ctimeMs) reject('source preimage changed');
  return bytes;
}
function absent(absolute) {
  try { fs.lstatSync(absolute); reject('occupied path'); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
}
function sync(absolute) {
  const fd = fs.openSync(absolute, 'r');
  try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
}
function syncTree(directory) {
  for (const name of fs.readdirSync(directory)) {
    const absolute = path.join(directory, name), info = fs.lstatSync(absolute);
    if (info.isDirectory()) syncTree(absolute);
    else if (info.isFile()) sync(absolute);
  }
  sync(directory);
}
function writeExclusive(absolute, bytes, permissions = 0o600) {
  fs.writeFileSync(absolute, bytes, { flag: 'wx', mode: permissions });
  if (!read(absolute).equals(Buffer.from(bytes))) reject('exclusive write readback');
  sync(absolute);
  sync(path.dirname(absolute));
}
for (const value of [root, prefix, agent, finalPrefix, finalRoot]) {
  if (typeof value !== 'string' || !/^\/[A-Za-z0-9_./-]+$/.test(value) || path.resolve(value) !== value) reject('unsafe selection');
}
if (process.argv.length !== 9 || !['separate', 'shared'].includes(mode) || !['install', 'verify', 'restore', 'prepare-prior'].includes(requestedAction) || uid === 0) reject('arguments or UID');
physical(root, true);
const state = path.join(root, 'state');
physical(state, true);
const node = path.join(root, 'runtime/node/bin/node');
const npm = path.join(root, 'runtime/node/lib/node_modules/npm/bin/npm-cli.js');
physical(node);
physical(npm);
const cache = path.join(root, 'runtime/cache');
let source = path.join(root, 'runtime/project');
const modules = path.join(prefix, 'lib/node_modules');
const settingsPath = path.join(agent, 'settings.json');
const upgradeState = path.join(state, 'upgrade');
const recoveryState = fs.existsSync(path.join(upgradeState, 'selection.json')) ? upgradeState : state;
const selectionPath = path.join(recoveryState, 'selection.json');
const priorGraph = path.join(state, 'prior-global-graph.json');
const env = {
  HOME: path.join(root, 'home'), TMPDIR: path.join(root, 'tmp'),
  XDG_CONFIG_HOME: path.join(root, 'config'), XDG_STATE_HOME: state,
  PATH: `${path.dirname(node)}:/usr/bin:/bin`, NODE_USE_SYSTEM_CA: '1',
  NPM_CONFIG_USERCONFIG: path.join(root, 'config/user.npmrc'),
  NPM_CONFIG_GLOBALCONFIG: path.join(root, 'config/global.npmrc'),
  NPM_CONFIG_IGNORE_SCRIPTS: 'true', npm_config_ignore_scripts: 'true',
  NPM_CONFIG_PREFIX: prefix, npm_config_prefix: prefix, NPM_CONFIG_CACHE: cache,
};
function stockNpm(args, cwd = source) {
  const child = spawnSync(node, [npm, ...args], {
    cwd, env, shell: false, timeout: 180000, killSignal: 'SIGKILL',
    maxBuffer: 8388608, encoding: 'utf8',
  });
  if (child.error || child.signal || child.status !== 0) {
    const output = (child.stdout ?? '') + (child.stderr ?? '');
    const detail = Buffer.byteLength(output) < 1024 && !output.includes('\0') && !output.includes('\ufffd')
      ? output : `entire npm output withheld; bytes=${Buffer.byteLength(output)}; sha256=${digest(Buffer.from(output))}`;
    reject(`stock npm ${args[0]} failed; status=${child.status}; signal=${child.signal}; error=${child.error?.code ?? ''}; ${detail}`);
  }
  return child.stdout;
}
function inventory(directory) {
  const records = [];
  let bytes = 0;
  function visit(absolute) {
    if (records.length >= 250000) reject('inventory entry bound');
    const info = fs.lstatSync(absolute);
    if (info.uid !== uid) reject('inventory foreign owner');
    const relative = path.relative(directory, absolute);
    if (info.isSymbolicLink()) {
      const target = fs.realpathSync(absolute);
      if (!target.startsWith(directory + '/')) reject('inventory escaping link');
      records.push([relative, 'link', fs.readlinkSync(absolute)]);
    } else if (info.isDirectory()) {
      physical(absolute, true);
      records.push([relative, 'dir', info.mode & 0o777]);
      for (const child of fs.readdirSync(absolute).sort()) visit(path.join(absolute, child));
    } else {
      if (!info.isFile()) reject('inventory special file');
      const raw = read(absolute);
      bytes += raw.length;
      if (bytes > 268435456) reject('inventory aggregate bound');
      records.push([relative, 'file', info.mode & 0o777, digest(raw)]);
    }
  }
  visit(directory);
  return digest(JSON.stringify(records));
}
function snapshot(upgrade = false) {
  const store = upgrade ? upgradeState : state;
  if (upgrade) {
    absent(store);
    fs.mkdirSync(store, { mode: 0o700 });
  }
  const before = { mode: upgrade ? 'shared' : mode, prefix, agent, finalPrefix, finalRoot, originalMode: mode };
  if (before.mode === 'shared') {
    physical(prefix, true);
    physical(agent, true);
    before.prefixSHA = inventory(prefix);
    before.agentSHA = inventory(agent);
    for (const [from, name] of [[prefix, 'prefix.preimage'], [agent, 'agent.preimage']]) {
      const to = path.join(store, name);
      absent(to);
      fs.cpSync(from, to, { recursive: true, force: false, errorOnExist: true, dereference: false, verbatimSymlinks: true });
      if (inventory(to) !== inventory(from)) reject('snapshot readback differs');
      syncTree(to);
    }
    if (inventory(prefix) !== before.prefixSHA || inventory(agent) !== before.agentSHA) reject('selected files changed during snapshot');
  }
  writeExclusive(path.join(store, 'selection.json'), `${JSON.stringify(before)}\n`);
}
function restore() {
  const selection = JSON.parse(read(selectionPath));
  if (selection.mode !== 'shared' || selection.prefix !== prefix || selection.agent !== agent || selection.finalRoot !== finalRoot || selection.finalPrefix !== finalPrefix || finalPrefix !== prefix) reject('recovery selection differs');
  for (const [target, name, expected] of [[prefix, 'prefix.preimage', selection.prefixSHA], [agent, 'agent.preimage', selection.agentSHA]]) {
    const saved = path.join(recoveryState, name);
    if (inventory(saved) !== expected) reject('recovery preimage differs');
    physical(target, true);
    if (inventory(target) === expected) continue;
    const quarantine = path.join(recoveryState, `${name}.quarantine-${crypto.randomUUID()}`);
    absent(quarantine);
    fs.mkdirSync(quarantine, { mode: 0o700 });
    // Same-filesystem renames preserve uncertain new evidence, not destructive rm.
    for (const entry of fs.readdirSync(target)) fs.renameSync(path.join(target, entry), path.join(quarantine, entry));
    fs.cpSync(saved, target, { recursive: true, force: false, errorOnExist: true, dereference: false, verbatimSymlinks: true });
    if (inventory(target) !== expected) reject('restored tree differs');
  }
  console.log('Shared preimages restored; uncertain new evidence retained');
}
if (action === 'restore') {
  restore();
} else {
  if (preparing) {
    const check = spawnSync(node, [process.argv[1], root, prefix, agent, finalPrefix, finalRoot, mode, 'verify'], { cwd: root, env, timeout: 60000, maxBuffer: 8388608 });
    if (check.error || check.signal || check.status !== 0) reject('fixture prior requires verified existing installation');
  }
  const prior = preparing || (fs.existsSync(priorGraph) && JSON.parse(read(path.join(modules, '@earendil-works/pi-coding-agent/package.json'))).version === '0.99.2');
  if (prior) {
    pins = { ...modernPins, '@earendil-works/pi-coding-agent': priorCoding };
    source = path.join(root, 'runtime/prior');
  }
  const graphPath = prior ? priorGraph : path.join(state, 'global-graph.json');
  if (action === 'install') {
    physical(agent, true);
    snapshot(preparing);
    for (const directory of (preparing ? [source] : [cache, source])) {
      absent(directory);
      fs.mkdirSync(directory, { mode: 0o700 });
    }
    const dependencies = Object.fromEntries(Object.entries(pins).map(([name, [version]]) => [name, version]));
    writeExclusive(path.join(source, 'package.json'), `${JSON.stringify({ name: 'gentle-shell-owned-seed', version: '1.0.0', private: true, dependencies })}\n`);
    stockNpm(['install', '--package-lock-only', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', '--min-release-age=0', '--registry=https://registry.npmjs.org/']);
    const { completeFile } = await import(pathToFileURL(path.join(root, 'complete-generated-lock-sri.mjs')).href);
    await completeFile(path.join(source, 'package-lock.json'));
  }
  const lockPath = path.join(source, 'package-lock.json');
  const lockBytes = read(lockPath);
  const lock = JSON.parse(lockBytes);
  if (lock.lockfileVersion !== 3 || !lock.packages || Object.keys(lock.packages[''].dependencies).length !== 5) reject('source lock');
  for (const [name, [version, integrity]] of Object.entries(pins)) {
    const entry = lock.packages[`node_modules/${name}`];
    if (entry?.version !== version || entry.integrity !== integrity || lock.packages[''].dependencies[name] !== version) reject('independent root pin');
  }
  if (prior) {
    const nested = lock.packages['node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui'];
    if (nested?.version !== '0.99.2' || nested.integrity !== priorTui) reject('prior nested TUI placement/pin');
  }
  if (action === 'install') {
    stockNpm(['ci', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', '--min-release-age=0', '--registry=https://registry.npmjs.org/']);
  }
  if (action === 'install') {
    const actual = [];
    function acquiredPackages(directory) {
      physical(directory, true);
      for (const name of fs.readdirSync(directory).sort()) {
        if (name === '.bin' || name === '.package-lock.json') continue;
        const absolute = path.join(directory, name);
        physical(absolute, true);
        if (name.startsWith('@')) acquiredPackages(absolute);
        else {
          actual.push(path.relative(source, absolute));
          if (actual.length > 4096) reject('acquired closure bound');
          const nested = path.join(absolute, 'node_modules');
          if (fs.existsSync(nested)) acquiredPackages(nested);
        }
      }
    }
    acquiredPackages(path.join(source, 'node_modules'));
    const matches = (rule, value) => {
      if (rule === undefined) return true;
      if (!Array.isArray(rule) || rule.some(item => typeof item !== 'string' || !/^!?[a-z0-9_]+$/.test(item))) reject('platform rule');
      return !rule.includes(`!${value}`) && !rule.includes('!any') && (!rule.some(item => !item.startsWith('!')) || rule.includes(value) || rule.includes('any'));
    };
    const expected = Object.entries(lock.packages).filter(([key, record]) => key && (!record.optional || (matches(record.os, 'linux') && matches(record.cpu, 'x64') && matches(record.libc, 'glibc')))).map(([key]) => key);
    const { normalize } = await import(pathToFileURL(path.join(root, 'normalize-private-optional-platform-closure.mjs')).href);
    normalize({ root: source, lock, expectedPaths: expected, actualPaths: actual, platform: { os: 'linux', cpu: 'x64', libc: 'glibc' } });
  }
  const authority = new Map();
  for (const [relative, entry] of Object.entries(lock.packages)) {
    if (!relative) continue;
    if (!/^node_modules\/(?:@?[a-z0-9][a-z0-9._-]*\/)*[a-z0-9][a-z0-9._-]*$/.test(relative)) reject('lock path');
    const name = entry.name ?? relative.split('node_modules/').at(-1);
    const canonical = `https://registry.npmjs.org/${name}/-/${name.split('/').at(-1)}-${entry.version}.tgz`;
    if (!packageName.test(name) || entry.resolved !== canonical || !/^sha512-[A-Za-z0-9+/]{86}==$/.test(entry.integrity)) reject('source integrity');
    if (name === '@earendil-works/pi-tui' && entry.version === '0.99.2' && entry.integrity !== priorTui) reject('independent nested prior TUI pin');
    const metadata = path.join(source, relative, 'package.json');
    if (!fs.existsSync(metadata)) {
      if (!entry.optional) reject('required acquired metadata absent');
      continue;
    }
    const bytes = read(metadata);
    const identity = JSON.parse(bytes);
    if (identity.name !== name || identity.version !== entry.version) reject('source identity');
    const key = `${name}@${entry.version}`;
    const prior = authority.get(key);
    if (prior && (prior.integrity !== entry.integrity || !prior.bytes.equals(bytes))) reject('ambiguous acquired identity');
    authority.set(key, { bytes, integrity: entry.integrity, source: path.dirname(metadata) });
  }
  let priorSettings;
  try { priorSettings = read(settingsPath); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  const settings = priorSettings ? JSON.parse(priorSettings) : {};
  if (!settings || typeof settings !== 'object' || Array.isArray(settings) || (settings.packages !== undefined && !Array.isArray(settings.packages))) reject('malformed settings');
  const packageRoot = path.join(finalPrefix, 'lib/node_modules/gentle-pi');
  if (action === 'install' && !preparing) {
    if (settings.npmCommand !== undefined) reject('foreign npm override');
    for (const entry of settings.packages ?? []) {
      const value = typeof entry === 'string' ? entry : entry?.source;
      if (typeof value !== 'string' || value.startsWith('npm:gentle-pi') || value === packageRoot) reject('foreign Gentle declaration');
    }
    if (mode === 'separate') {
      absent(prefix);
      fs.mkdirSync(prefix, { mode: 0o700 });
    }
  }
  const observed = [];
  let comparedFiles = 0;
  function sourceBytes(directory, acquired, gentle = false) {
    const include = name => name !== 'node_modules' && !(gentle && name === '.gentle-ai');
    const entries = fs.readdirSync(directory).filter(include).sort();
    const expected = fs.readdirSync(acquired).filter(include).sort();
    if (JSON.stringify(entries) !== JSON.stringify(expected)) reject('global source file set differs');
    for (const name of entries) {
      const actual = path.join(directory, name), original = path.join(acquired, name);
      if (fs.lstatSync(actual).isDirectory()) {
        physical(actual, true);
        physical(original, true);
        sourceBytes(actual, original);
      } else if (++comparedFiles > 250000 || !read(actual).equals(read(original))) reject('global source bytes differ');
    }
  }
  function packages(directory) {
    physical(directory, true);
    for (const name of fs.readdirSync(directory).sort()) {
      if (name === '.bin' || name === '.package-lock.json') continue;
      const entry = path.join(directory, name);
      physical(entry, true);
      if (name.startsWith('@')) {
        for (const leaf of fs.readdirSync(entry).sort()) visit(path.join(entry, leaf));
      } else visit(entry);
    }
  }
  function visit(directory) {
    if (observed.length >= 4096) reject('global graph bound');
    physical(directory, true);
    const bytes = read(path.join(directory, 'package.json'));
    const metadata = JSON.parse(bytes);
    const acquired = authority.get(`${metadata.name}@${metadata.version}`);
    if (!acquired || !bytes.equals(acquired.bytes)) reject('global package lacks authenticated metadata');
    sourceBytes(directory, acquired.source, metadata.name === 'gentle-pi');
    observed.push({ directory: path.relative(prefix, directory), name: metadata.name, version: metadata.version, integrity: acquired.integrity });
    const nested = path.join(directory, 'node_modules');
    if (fs.existsSync(nested)) packages(nested);
  }
  function nativeState() {
    const native = path.join(modules, 'gentle-pi/.gentle-ai');
    if (!fs.existsSync(native)) return;
    physical(native, true);
    const entries = fs.readdirSync(native).sort();
    if (entries.length === 0) return;
    if (JSON.stringify(entries) !== '["v4.0.0"]') reject('foreign native recovery state');
    const version = path.join(native, 'v4.0.0');
    physical(version, true);
    if (JSON.stringify(fs.readdirSync(version).sort()) !== '["gentle-ai","integrity.json"]') reject('native file set');
    const binary = read(path.join(version, 'gentle-ai'));
    const manifest = '{"version":"4.0.0","asset":"gentle-ai_4.0.0_linux_amd64.tar.gz","assetSha256":"5f4417cf29c969c86da4799942fd673368840901be1bb09c779a12d7ed6096ea","binarySha256":"50ba217b5138c1a9c7d5bf2f79931b1bb89b89c4cf650dcd7ee037657c88158d"}\n';
    if (binary.length !== 17109176 || digest(binary) !== '50ba217b5138c1a9c7d5bf2f79931b1bb89b89c4cf650dcd7ee037657c88158d' || !read(path.join(version, 'integrity.json')).equals(Buffer.from(manifest))) reject('native independent readback');
  }
  if (action === 'install') {
    nativeState();
    if (mode === 'shared' && !preparing) packages(modules);
    observed.length = 0;
    stockNpm(['install', '--global', '--prefix', prefix, '--offline', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', ...(preparing ? [`@earendil-works/pi-coding-agent@${priorCoding[0]}`] : Object.entries(pins).map(([name, [version]]) => `${name}@${version}`))]);
  }
  packages(modules);
  const semver = createRequire(npm)('semver'); // Authenticated stock Node/npm closure.
  function resolve(directory, name) {
    for (let parent = directory; parent.startsWith(modules); parent = path.dirname(parent)) {
      const candidate = path.join(parent, 'node_modules', name);
      if (observed.some(record => path.join(prefix, record.directory) === candidate)) return candidate;
    }
    const candidate = path.join(modules, name);
    if (observed.some(record => path.join(prefix, record.directory) === candidate)) return candidate;
  }
  for (const item of observed) {
    const directory = path.join(prefix, item.directory), metadata = JSON.parse(read(path.join(directory, 'package.json')));
    for (const [declarations, peer] of [[metadata.dependencies ?? {}, false], [metadata.peerDependencies ?? {}, true]]) {
      for (const [name, range] of Object.entries(declarations)) {
        if (!packageName.test(name) || typeof range !== 'string') reject('dependency declaration');
        const target = resolve(directory, name);
        const optional = peer ? metadata.peerDependenciesMeta?.[name]?.optional : metadata.optionalDependencies?.[name];
        if (!target && optional) continue;
        if (!target || !semver.satisfies(JSON.parse(read(path.join(target, 'package.json'))).version, range)) reject('global required/peer range mismatch');
      }
    }
    for (const bins of [path.join(directory, 'node_modules/.bin'), ...(directory === path.join(modules, 'gentle-pi') ? [path.join(modules, '.bin')] : [])]) {
      if (!fs.existsSync(bins)) continue;
      physical(bins, true);
      for (const name of fs.readdirSync(bins)) {
        const link = path.join(bins, name), target = fs.realpathSync(link);
        if (!fs.lstatSync(link).isSymbolicLink() || !target.startsWith(modules + '/')) reject('foreign nested bin');
        read(target);
      }
    }
  }
  for (const [name, [version]] of Object.entries(pins)) {
    const metadata = JSON.parse(read(path.join(modules, name, 'package.json')));
    if (metadata.name !== name || metadata.version !== version) reject('global root mutation');
    const bins = typeof metadata.bin === 'string' ? { [name.split('/').at(-1)]: metadata.bin } : metadata.bin ?? {};
    for (const [command, relative] of Object.entries(bins)) {
      if (!/^[A-Za-z0-9._-]+$/.test(command) || typeof relative !== 'string') reject('root bin declaration');
      const link = path.join(prefix, 'bin', command), expected = path.resolve(modules, name, relative);
      if (!expected.startsWith(path.join(modules, name) + '/') || !fs.lstatSync(link).isSymbolicLink() || fs.realpathSync(link) !== expected) reject('root bin binding');
      read(expected);
    }
  }
  const hidden = path.join(modules, '.package-lock.json');
  if (fs.existsSync(hidden)) {
    const installed = JSON.parse(read(hidden));
    if (installed.lockfileVersion !== 3 || !installed.packages) reject('global hidden lock');
    for (const [relative, record] of Object.entries(installed.packages)) {
      if (!observed.some(item => item.directory === path.posix.join('lib', relative) && item.version === record.version && item.integrity === record.integrity)) reject('global hidden lock differs');
    }
  }
  const supplier = path.join(modules, 'gentle-pi');
  for (const [relative, expected] of [
    ['scripts/gentle-ai-installer.mjs', 'bc2da0585026fa538f0c6ae0cf50463767c175b71d0dfdb582a88cbe894c84ca'],
    ['runtime/gentle-ai-binary.mjs', 'cbdf5deac8b7a85ab1253dbd049953aeb192a7d1f7987f9206916ab449c10a92'],
  ]) {
    if (digest(read(path.join(supplier, relative))) !== expected) reject('stock supplier pin');
  }
  nativeState();
  if (action === 'install' && !preparing) {
    let fresh;
    try { fresh = read(settingsPath); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (Boolean(fresh) !== Boolean(priorSettings) || (fresh && !fresh.equals(priorSettings))) reject('settings changed');
    settings.packages = [...(settings.packages ?? []), packageRoot];
    settings.npmCommand = [path.join(finalRoot, 'runtime/node/bin/node'), path.join(finalRoot, 'runtime/node/lib/node_modules/npm/bin/npm-cli.js'), '--prefix', finalPrefix];
    const stage = path.join(agent, `.gentle-shell-settings-${process.pid}`);
    writeExclusive(stage, `${JSON.stringify(settings, null, 2)}\n`);
    fs.renameSync(stage, settingsPath);
    writeExclusive(path.join(state, 'global-graph.json'), `${JSON.stringify(observed)}\n`);
  } else {
    const declaration = settings.packages?.some(entry => (typeof entry === 'string' ? entry : entry?.source) === packageRoot);
    const command = [path.join(finalRoot, 'runtime/node/bin/node'), path.join(finalRoot, 'runtime/node/lib/node_modules/npm/bin/npm-cli.js'), '--prefix', finalPrefix];
    if (!declaration || JSON.stringify(settings.npmCommand) !== JSON.stringify(command)) reject('selected settings bindings changed');
    if (preparing) {
      if (!read(settingsPath).equals(priorSettings)) reject('fixture changed selected settings');
      writeExclusive(graphPath, `${JSON.stringify(observed)}\n`);
    }
    else if (!read(graphPath).equals(Buffer.from(`${JSON.stringify(observed)}\n`))) reject('global graph changed; inspect update before relaunch');
  }
  if (!read(lockPath).equals(lockBytes)) reject('acquired lock changed');
  console.log(`Global readback verified; authenticated packages=${observed.length}; functional Ready=false`);
}
