// Windows Separate only. Stock npm, explicit stock native installer, no updater.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const [root, action] = process.argv.slice(2);
const reject = message => { throw Error(`Windows provision refused: ${message}`); };
if (process.platform !== 'win32' || process.arch !== 'x64' || process.argv.length !== 4 || !['install', 'verify'].includes(action) || !path.isAbsolute(root)) reject('platform/arguments');
const digest = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const node = path.join(root, 'runtime/node/node.exe');
const npm = path.join(root, 'runtime/node/node_modules/npm/bin/npm-cli.js');
const prefix = path.join(root, 'prefix');
const modules = path.join(prefix, 'node_modules');
const source = path.join(root, 'source');
const agent = path.join(root, 'agent');
const pins = {
  'gentle-pi': ['4.0.0', 'sha512-ZG/diWBSKPfjU4MjiHVUXxHWQvDSRS2dvpPCma4ZINuV+8UGdZAPHca6vcK27FLAAG7crlJpwdWOcwVNW4rh/Q=='],
  '@earendil-works/pi-coding-agent': ['1.0.0', 'sha512-/FtbxoSQU/mEv1QnichJjRjqteqaIaMWxmhB4G367+MwZfX7/DI5B9YAg5lqbN7nztFskBEtUSZ+FlmMBECtMw=='],
  '@earendil-works/pi-tui': ['1.0.0', 'sha512-JsT7kXnpZA2YOtQu6RyriyxEO0eJIzPyfiH09bH+OLN5+s18HYkwaUD/tBkjhnSfMu6/50CQPRYJagzSP6HdPw=='],
  '@heyhuynhgiabuu/pi-pretty': ['0.6.27', 'sha512-4Jj+n6ZBFdn979fWAA3nMcJ45Q5qtcLeq1Pe6+Oo2LDIpDhqv7heoTKkBpq9G74/pNYc0EaXR28pssQ5Wbc5bg=='],
  'typebox': ['1.3.27', 'sha512-zu+jc1pcy4UiNThxikUr36f0Rybk9PEeCg/NE6adeWr/SKsdNO4EzZHYRDlv2YCVAfj3Odq3dESSo/jNyoBXzA=='],
};
// NTFS owner/DACL/reparse checks are enforced by the Go worker before entry and
// on the complete produced tree afterward. This is not a substitute for them.
function read(file) {
  const before = fs.lstatSync(file);
  if (!before.isFile() || before.isSymbolicLink() || before.size > 33554432) reject('physical file/bound');
  const value = fs.readFileSync(file);
  const after = fs.lstatSync(file);
  if (before.ino !== after.ino || before.size !== after.size || before.mtimeMs !== after.mtimeMs || before.ctimeMs !== after.ctimeMs) reject('file preimage differs');
  return value;
}
function exclusive(file, value) {
  fs.writeFileSync(file, value, { flag: 'wx' });
  if (!read(file).equals(Buffer.from(value))) reject('exclusive readback');
}
function npmCommand(args) {
  const child = spawnSync(node, [npm, ...args], { cwd: source, env: process.env, shell: false, timeout: 180000, maxBuffer: 8388608 });
  if (child.error || child.status !== 0 || child.signal) {
    const bytes = Buffer.concat([child.stdout ?? Buffer.alloc(0), child.stderr ?? Buffer.alloc(0)]);
    const text = bytes.toString('utf8');
    const detail = bytes.length <= 8192 && Buffer.from(text, 'utf8').equals(bytes) && !text.includes('\0')
      ? JSON.stringify(text).replace(/[\u007f-\u009f]/g, char => `\\u${char.charCodeAt(0).toString(16).padStart(4, '0')}`)
      : 'whole npm diagnostic withheld (size/encoding guard)';
    reject(`npm ${args[0]} failed; status=${child.status}; error=${child.error?.code ?? 'none'}; bytes=${bytes.length}; sha256=${digest(bytes)}; diagnostic=${detail}`);
  }
}
const lockPath = path.join(source, 'package-lock.json');
if (action === 'install') {
  fs.mkdirSync(source);
  exclusive(path.join(source, 'package.json'), JSON.stringify({ name: 'gentle-shell-windows-private', private: true, version: '1.0.0', dependencies: Object.fromEntries(Object.entries(pins).map(([name, [version]]) => [name, version])) }));
  npmCommand(['install', '--package-lock-only', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', '--min-release-age=0', '--registry=https://registry.npmjs.org/']);
  const { completeFile } = await import(pathToFileURL(path.join(root, 'complete-generated-lock-sri.mjs')).href);
  await completeFile(lockPath);
}
const lockBytes = read(lockPath), lock = JSON.parse(lockBytes);
if (lock.lockfileVersion !== 3 || !lock.packages || Object.keys(lock.packages).length > 4096 || Object.keys(lock.packages[''].dependencies).length !== 5) reject('source lock');
for (const [name, [version, integrity]] of Object.entries(pins)) {
  const record = lock.packages[`node_modules/${name}`];
  if (record?.version !== version || record.integrity !== integrity || lock.packages[''].dependencies[name] !== version) reject('root SRI/version');
}
for (const [relative, record] of Object.entries(lock.packages)) {
  if (!relative) continue;
  if (!/^node_modules\/(?:@?[a-z0-9][a-z0-9._-]*\/)*[a-z0-9][a-z0-9._-]*$/.test(relative)) reject('lock placement');
  const name = record.name ?? relative.split('node_modules/').at(-1);
  if (record.resolved !== `https://registry.npmjs.org/${name}/-/${name.split('/').at(-1)}-${record.version}.tgz` || !/^sha512-[A-Za-z0-9+/]{86}==$/.test(record.integrity)) reject('locked supplier');
}
if (action === 'install') {
  npmCommand(['ci', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', '--min-release-age=0', '--registry=https://registry.npmjs.org/']);
  npmCommand(['install', '--global', '--prefix', prefix, '--offline', '--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', ...Object.entries(pins).map(([name, [version]]) => `${name}@${version}`)]);
}
const authority = new Map();
for (const [relative, record] of Object.entries(lock.packages)) {
  if (!relative) continue;
  const metadata = path.join(source, relative, 'package.json');
  if (!fs.existsSync(metadata)) {
    if (record.optional !== true) reject('required acquired source absent');
    continue;
  }
  const bytes = read(metadata), identity = JSON.parse(bytes);
  const name = record.name ?? relative.split('node_modules/').at(-1);
  if (identity.name !== name || identity.version !== record.version) reject('acquired identity');
  const key = `${name}@${record.version}`, previous = authority.get(key);
  if (previous && (!previous.bytes.equals(bytes) || previous.integrity !== record.integrity)) reject('ambiguous acquired source');
  authority.set(key, { directory: path.dirname(metadata), bytes, integrity: record.integrity });
}
let count = 0, compared = 0, bytes = 0;
function compare(actual, original, gentle = false) {
  const names = directory => fs.readdirSync(directory).filter(name => name !== 'node_modules' && !(gentle && name === '.gentle-ai')).sort();
  const a = names(actual), b = names(original);
  if (JSON.stringify(a) !== JSON.stringify(b)) reject('global source set differs');
  for (const name of a) {
    if (++compared > 250000) reject('source file count');
    const left = path.join(actual, name), right = path.join(original, name);
    const info = fs.lstatSync(left);
    if (info.isSymbolicLink()) reject('source alias');
    if (info.isDirectory()) compare(left, right);
    else {
      const value = read(left);
      bytes += value.length;
      if (bytes > 1073741824 || !value.equals(read(right))) reject('source bytes/bound');
    }
  }
}
function packages(directory) {
  for (const name of fs.readdirSync(directory).sort()) {
    if (name === '.bin' || name === '.package-lock.json') continue;
    const absolute = path.join(directory, name);
    const info = fs.lstatSync(absolute);
    if (!info.isDirectory() || info.isSymbolicLink()) reject('global package alias/type');
    if (name.startsWith('@')) { packages(absolute); continue; }
    if (++count > 4096) reject('global graph bound');
    const metadata = read(path.join(absolute, 'package.json')), record = JSON.parse(metadata);
    const acquired = authority.get(`${record.name}@${record.version}`);
    if (!acquired || !metadata.equals(acquired.bytes)) reject('global unauthenticated metadata');
    compare(absolute, acquired.directory, record.name === 'gentle-pi');
    if (fs.existsSync(path.join(absolute, 'node_modules'))) packages(path.join(absolute, 'node_modules'));
  }
}
packages(modules);
const gentle = path.join(modules, 'gentle-pi');
for (const [name, expected] of [
  ['scripts/gentle-ai-installer.mjs', 'bc2da0585026fa538f0c6ae0cf50463767c175b71d0dfdb582a88cbe894c84ca'],
  ['runtime/gentle-ai-binary.mjs', 'cbdf5deac8b7a85ab1253dbd049953aeb192a7d1f7987f9206916ab449c10a92'],
]) if (digest(read(path.join(gentle, name))) !== expected) reject('stock native supplier source pin');
if (action === 'install') {
  const { installGentleAi } = await import(pathToFileURL(path.join(gentle, 'scripts/gentle-ai-installer.mjs')).href);
  await installGentleAi({ packageRoot: gentle }); // Explicit, authenticated stock API, no postinstall/fullscreen hook.
}
const native = path.join(gentle, '.gentle-ai/v4.0.0');
const manifest = JSON.parse(read(path.join(native, 'integrity.json')));
if (manifest.version !== '4.0.0' || manifest.method !== 'go-sumdb-source-build' || manifest.moduleChecksum !== 'h1:pZ/XZ2Pk3U9lgXigOTY62zlxxFOHnc9CjQhLgaV/Hfc=' || manifest.binarySha256 !== digest(read(path.join(native, 'gentle-ai.exe')))) reject('stock native source manifest');
const settingsPath = path.join(agent, 'settings.json');
const finalRoot = JSON.parse(read(path.join(root, 'selection.json'))).Destination;
if (!path.isAbsolute(finalRoot)) reject('publication selection');
const settings = { packages: [path.join(finalRoot, 'prefix/node_modules/gentle-pi')], npmCommand: [path.join(finalRoot, 'runtime/node/node.exe'), path.join(finalRoot, 'runtime/node/node_modules/npm/bin/npm-cli.js'), '--prefix', path.join(finalRoot, 'prefix')] };
if (action === 'install') exclusive(settingsPath, `${JSON.stringify(settings, null, 2)}\n`);
const observedSettings = JSON.parse(read(settingsPath));
const settingsKeys = ['packages', 'npmCommand', 'lastChangelogVersion'];
if (observedSettings === null || typeof observedSettings !== 'object' || Array.isArray(observedSettings) ||
    Object.keys(observedSettings).some(key => !settingsKeys.includes(key)) ||
    (Object.hasOwn(observedSettings, 'lastChangelogVersion') && typeof observedSettings.lastChangelogVersion !== 'string') ||
    JSON.stringify({ packages: observedSettings.packages, npmCommand: observedSettings.npmCommand }) !== JSON.stringify(settings)) {
  reject('owned package/settings bindings changed');
}
if (!read(lockPath).equals(lockBytes)) reject('source lock changed');
console.log(`Windows stock composition verified; packages=${count}; registration and full Ready remain unqualified`);
