#!/bin/sh
# Component installation only. No package entrypoint, Pi launch, or Ready claim.
set -eu
umask 077
fail() { printf 'STOP: %s\n' "$1" >&2; exit 1; }
test "$#" = 6 || fail 'expected --destination PATH --bundle PATH --bundle-sha256 DIGEST'
test "$1" = --destination && test "$3" = --bundle && test "$5" = --bundle-sha256 || fail 'invalid arguments'
destination=$2 bundle=$4 digest=$6
case "$destination" in /*) ;; *) fail 'destination must be absolute' ;; esac
case "$bundle" in /*) ;; *) fail 'bundle must be absolute' ;; esac
case "$digest" in *[!0-9a-f]*|'') fail 'invalid bundle digest' ;; esac
test "${#digest}" = 64 || fail 'invalid bundle digest length'
parent=$(dirname -- "$destination")
name=$(basename -- "$destination")
case "$name" in .|..|/|*[!A-Za-z0-9_.-]*) fail 'invalid destination' ;; esac
test -d "$parent" && test ! -L "$parent" || fail 'destination parent absent or symlink'
test "$(realpath -- "$parent")" = "$parent" || fail 'destination parent is not canonical'
test "$(stat -c %u "$parent")" = "$(id -u)" || fail 'destination parent not owned'
test "$(stat -c %a "$parent")" = 700 || fail 'destination parent must be private mode 0700'
test ! -e "$destination" && test ! -L "$destination" || fail 'destination already exists'
# Verify all runtime/cache bytes using stock tools before invoking bundled Node.
test -f "$bundle/SHA256SUMS" && test ! -L "$bundle/SHA256SUMS" || fail 'bundle manifest absent'
test "$(sha256sum "$bundle/SHA256SUMS" | cut -d ' ' -f1)" = "$digest" || fail 'bundle manifest digest mismatch'
awk 'length($1)!=64 || $1 ~ /[^0-9a-f]/ || NF!=2 || $2 !~ /^(node|cache|project)\/[A-Za-z0-9_@.+\/-]+$/ || $2 ~ /(^|\/)\.\.?(\/|$)/ || seen[$2]++ {bad=1} END {exit bad}' "$bundle/SHA256SUMS" || fail 'unsafe manifest'
test -z "$(find "$bundle" ! -type f ! -type d -print -quit)" || fail 'nonregular bundle object'
actual=$(cd "$bundle" && find . -type f ! -path ./SHA256SUMS -printf '%P\n' | LC_ALL=C sort)
listed=$(awk '{print $2}' "$bundle/SHA256SUMS" | LC_ALL=C sort)
test "$actual" = "$listed" || fail 'bundle inventory differs'
(cd "$bundle" && sha256sum --strict -c SHA256SUMS >/dev/null) || fail 'bundle byte mismatch'
node=$bundle/node/bin/node
npm=$bundle/node/lib/node_modules/npm/bin/npm-cli.js
test -x "$node" || printf 'runtime guard node-mode=%s tmp-options=%s\n' "$(stat -c %a "$node")" "$(awk '$5=="/tmp" {print $6}' /proc/self/mountinfo)"
test -x "$node" || fail 'private Node not executable'
test -f "$npm" || fail 'private npm CLI absent'
# No inherited HOME, npm configuration, loader flags, or global Pi search path.
stage=$(mktemp -d "$parent/.gentle-shell-stage.XXXXXXXX")
cleanup() { test -z "${stage:-}" || rm -rf -- "$stage"; }
trap cleanup EXIT HUP INT TERM
cp -R "$bundle/node" "$bundle/cache" "$bundle/project" "$stage/"
test -z "$(find "$stage" ! -type f ! -type d -print -quit)" || fail 'nonregular copied bundle object'
copied=$(cd "$stage" && find . -type f -printf '%P\n' | LC_ALL=C sort)
test "$copied" = "$listed" || fail 'copied bundle inventory differs'
cp "$bundle/SHA256SUMS" "$stage/SHA256SUMS"
test "$(sha256sum "$stage/SHA256SUMS" | cut -d ' ' -f1)" = "$digest" || fail 'copied manifest digest differs'
(cd "$stage" && sha256sum --strict -c SHA256SUMS >/dev/null) || fail 'copied bundle byte mismatch'
mkdir "$stage/home" "$stage/config" "$stage/state" "$stage/tmp"
: > "$stage/config/user.npmrc"
: > "$stage/config/global.npmrc"
node=$stage/node/bin/node
npm=$stage/node/lib/node_modules/npm/bin/npm-cli.js
test "$(env -i "$node" --version)" = v24.18.0 || fail 'private Node version differs'
private_npm() {
    env -i HOME="$stage/home" TMPDIR="$stage/tmp" XDG_CONFIG_HOME="$stage/config" XDG_STATE_HOME="$stage/state" \
        PATH="$stage/node/bin:/usr/bin:/bin" NPM_CONFIG_USERCONFIG="$stage/config/user.npmrc" \
        NPM_CONFIG_GLOBALCONFIG="$stage/config/global.npmrc" "$node" "$npm" \
        --cache "$stage/cache" --ignore-scripts --engine-strict --no-audit --no-fund "$@"
}
test "$(private_npm --version)" = 11.16.0 || fail 'private npm version differs'
# Inspect the complete lock before the actual offline install. Never load package JS.
env -i "$node" --input-type=module - "$stage/project" <<'JS'
import fs from 'node:fs';
const root = process.argv[2];
const lock = JSON.parse(fs.readFileSync(`${root}/package-lock.json`));
const pins = {
  'gentle-pi': ['3.7.0', 'sha512-SXBp9jIRnVIcOsLCW/Zw4XDTXxhViXNxrCZS7LyioB6kdRl99Ohojeaj+yIH5+K/ucwLlTlHYBz8zwue9aspNQ=='],
  '@earendil-works/pi-coding-agent': ['0.85.1', 'sha512-FGRN+OHbWaefBPGaTggAdLjrIHW+s2PzLyglz/5dfLzb9of7uuXMXYC0fJIeZTw+shS32o2cuQ9jF7YSDuL/oQ=='],
  '@earendil-works/pi-tui': ['0.85.1', 'sha512-OIzw9efInmO4WOBnD4TxcTdBjmzvYJpzslkgoUro946nEGoYWg5rwv1p4fDt3/JvMx9QybryUCUwlm7j8Dreig=='],
  '@heyhuynhgiabuu/pi-pretty': ['0.6.27', 'sha512-4Jj+n6ZBFdn979fWAA3nMcJ45Q5qtcLeq1Pe6+Oo2LDIpDhqv7heoTKkBpq9G74/pNYc0EaXR28pssQ5Wbc5bg=='],
  'typebox': ['1.3.7', 'sha512-meKuifc33Pccx0O6PdIzYMq3Og8zvP4TIi/a+Bw3AEMZMxOD0+RHGQvpglEe6Zdy3wZ8nqn/j95h8LUZLk/6Hg==']
};
const reject = message => { throw Error(message); };
if (lock.lockfileVersion !== 3 || !lock.packages) reject('unsupported lock');
const manifest = JSON.parse(fs.readFileSync(`${root}/package.json`));
if (Object.keys(manifest.dependencies || {}).length !== 5 ||
    Object.keys(lock.packages[''].dependencies || {}).length !== 5) reject('root manifest differs');
for (const [name, [version, integrity]] of Object.entries(pins)) {
  const p = lock.packages[`node_modules/${name}`];
  if (!p || p.version !== version || p.integrity !== integrity ||
      lock.packages[''].dependencies[name] !== version ||
      manifest.dependencies[name] !== version) reject(`root pin differs: ${name}`);
}
const packageName = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;
const packagePath = /^(?:node_modules\/(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*)(?:\/node_modules\/(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*)*$/;
const libc = process.platform === 'linux' ? (process.report.getReport().header.glibcVersionRuntime ? 'glibc' : 'musl') : undefined;
function matches(rule, value) {
  if (rule === undefined) return true;
  if (!Array.isArray(rule) || rule.some(x => typeof x !== 'string' || !/^!?[a-z0-9_]+$/.test(x))) reject('invalid platform rule');
  return !rule.includes(`!${value}`) && !rule.includes('!any') &&
    (!rule.some(x => !x.startsWith('!')) || rule.includes(value) || rule.includes('any'));
}
const expected = [];
for (const [path, p] of Object.entries(lock.packages)) {
  if (!path) continue;
  if (!packagePath.test(path) || p.link ||
      !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(p.version) ||
      !/^https:\/\/registry\.npmjs\.org\/[A-Za-z0-9@%/_.-]+\.tgz$/.test(p.resolved) ||
      !/^sha512-[A-Za-z0-9+/]{86}==$/.test(p.integrity)) reject(`unsafe closure: ${path}`);
  const name = p.name ?? path.split('node_modules/').at(-1);
  if (!packageName.test(name)) reject(`unsafe package name: ${path}`);
  const leaf = name.split('/').at(-1);
  if (p.resolved !== `https://registry.npmjs.org/${name}/-/${leaf}-${p.version}.tgz`) reject(`noncanonical URL: ${path}`);
  const applicable = [matches(p.os, process.platform), matches(p.cpu, process.arch), matches(p.libc, libc)].every(Boolean);
  if (!applicable && !p.optional) reject(`required incompatible package: ${path}`);
  if (applicable) expected.push(path);
}
fs.writeFileSync(`${root}/../closure.json`, JSON.stringify(expected));
let lifecycle = 0, native = 0;
for (const [path, p] of Object.entries(lock.packages)) {
  if (!path) continue;
  const metadataPath = `${root}/${path}/package.json`;
  if (!fs.existsSync(metadataPath)) {
    if (expected.includes(path)) reject(`missing applicable acquired metadata: ${path}`);
    continue;
  }
  const m = JSON.parse(fs.readFileSync(metadataPath));
  if (m.version !== p.version) reject(`acquired version differs: ${path}`);
  lifecycle += Number(Boolean(m.scripts?.install || m.scripts?.preinstall || m.scripts?.postinstall));
  native += Number(Boolean(m.gypfile || fs.existsSync(`${root}/${path}/binding.gyp`)));
}
console.log(`lock validated packages=${Object.keys(lock.packages).length - 1} lifecycle-disabled=${lifecycle} native-declarations=${native}`);
JS
before_lock=$(sha256sum "$stage/project/package-lock.json" | cut -d ' ' -f1)
printf 'command: private-node npm-cli ci --offline --ignore-scripts --engine-strict --no-audit --no-fund lock-sha256=%s\n' "$before_lock"
if ! (cd "$stage/project" && private_npm ci --offline >"$stage/npm.log" 2>&1); then
    test "$(wc -c < "$stage/npm.log")" -gt 4096 || awk '{print "npm diagnostic: " $0}' "$stage/npm.log"
    fail 'offline npm install failed'
fi
test "$(sha256sum "$stage/project/package-lock.json" | cut -d ' ' -f1)" = "$before_lock" || fail 'install changed lock'
# Data-only inventory verifies install output, including lifecycle/native metadata.
env -i "$node" --input-type=module - "$stage/project" <<'JS'
import fs from 'node:fs';
const root = process.argv[2];
const lock = JSON.parse(fs.readFileSync(`${root}/package-lock.json`));
const expected = JSON.parse(fs.readFileSync(`${root}/../closure.json`)).sort();
const actual = [];
function directory(path) {
  if (!fs.lstatSync(`${root}/${path}`).isDirectory()) throw Error(`non-directory or symlink package: ${path}`);
}
function walk(modules) {
  if (!fs.existsSync(`${root}/${modules}`)) return;
  directory(modules);
  for (const entry of fs.readdirSync(`${root}/${modules}`)) {
    if (entry === '.bin' || entry === '.package-lock.json') continue;
    const path = `${modules}/${entry}`;
    directory(path);
    const packages = entry.startsWith('@') ? fs.readdirSync(`${root}/${path}`).map(x => `${path}/${x}`) : [path];
    for (const packagePath of packages) {
      directory(packagePath);
      actual.push(packagePath);
      walk(`${packagePath}/node_modules`);
    }
  }
}
walk('node_modules');
actual.sort();
if (JSON.stringify(actual) !== JSON.stringify(expected)) throw Error('installed closure set differs');
console.log(`closure expected=${expected.length} actual=${actual.length}`);
let count = 0, lifecycle = 0, native = 0;
for (const path of expected) {
  const p = lock.packages[path];
  const metadataPath = `${root}/${path}/package.json`;
  if (!fs.lstatSync(metadataPath).isFile()) throw Error(`nonregular metadata: ${path}`);
  const metadata = JSON.parse(fs.readFileSync(metadataPath));
  if (metadata.version !== p.version || metadata.name !== (p.name ?? path.split('node_modules/').at(-1))) throw Error(`installed identity differs: ${path}`);
  lifecycle += Number(Boolean(metadata.scripts?.install || metadata.scripts?.postinstall || metadata.scripts?.preinstall));
  native += Number(Boolean(metadata.gypfile || fs.existsSync(`${root}/${path}/binding.gyp`)));
  count++;
}
for (const name of ['gentle-pi', '@earendil-works/pi-coding-agent', '@earendil-works/pi-tui', '@heyhuynhgiabuu/pi-pretty', 'typebox']) {
  const m = JSON.parse(fs.readFileSync(`${root}/node_modules/${name}/package.json`));
  console.log(`installed ${name}@${m.version}`);
}
console.log(`installed inventory packages=${count} lifecycle-disabled=${lifecycle} native-declarations=${native}`);
JS
lock_digest=$(sha256sum "$stage/project/package-lock.json" | cut -d ' ' -f1)
# Parent is privately owned: no replacement or update of existing installation.
test ! -e "$destination" && test ! -L "$destination" || fail 'destination appeared during install'
mv -T --no-clobber "$stage" "$destination"
test ! -e "$stage" || fail 'publication refused'
stage=
printf 'installation exit=0 Node=24.18.0 npm=11.16.0 prefix=%s lock-sha256=%s Ready=false\n' "$destination" "$lock_digest"
