// Guest-only fixtures for the actual provisioner's settings region, not SDK/source-graph qualification.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';

assert.equal(process.platform, 'win32', 'fixture execution requires the qualified Windows Guest');
assert.equal(process.argv.length, 4, 'supply installed provision.mjs and a fresh private report path');
const source = fs.readFileSync(process.argv[2], 'utf8');
const begin = "const settingsPath = path.join(agent, 'settings.json');";
const end = "if (!read(lockPath).equals(lockBytes)) reject('source lock changed');";
const start = source.indexOf(begin), finish = source.indexOf(end);
assert.ok(start >= 0 && finish > start, 'settings-region boundaries must exist');
assert.equal(source.indexOf(begin, start + begin.length), -1, 'settings-region start must be unique');
assert.equal(source.indexOf(end, finish + end.length), -1, 'settings-region end must be unique');
const body = source.slice(start, finish);
const root = 'R:\\Owned Settings Fixture', agent = path.join(root, 'agent');
const expected = {
  packages: [path.join(root, 'prefix/node_modules/gentle-pi')],
  npmCommand: [path.join(root, 'runtime/node/node.exe'), path.join(root, 'runtime/node/node_modules/npm/bin/npm-cli.js'), '--prefix', path.join(root, 'prefix')],
};
const refusal = 'Windows provision refused: owned package/settings bindings changed';
const copy = () => JSON.parse(JSON.stringify(expected));
const cases = [
  { name: 'baseline', value: copy(), want: 'accept' },
  { name: 'sdk-changelog-version', value: { ...copy(), lastChangelogVersion: '1.0.0' }, want: 'accept' },
  { name: 'changed-packages', value: { ...copy(), packages: ['R:\\Foreign Package'] }, want: 'reject' },
  { name: 'changed-npm-command', value: { ...copy(), npmCommand: ['R:\\Foreign Executable'] }, want: 'reject' },
  { name: 'missing-packages', value: { npmCommand: expected.npmCommand }, want: 'reject' },
  { name: 'missing-npm-command', value: { packages: expected.packages }, want: 'reject' },
  { name: 'null-settings', value: null, want: 'reject' },
  { name: 'array-settings', value: [], want: 'reject' },
  { name: 'install-readback', value: null, action: 'install', want: 'accept' },
];
for (const key of ['extensions', 'skills', 'customProviders', 'constructor', '__proto__', 'toString']) {
  cases.push({ name: `injected-${key}`, value: { ...copy(), [key]: ['R:\\Foreign Resource'] }, want: 'reject' });
}
for (const [name, value] of [['null', null], ['number', 1], ['array', []], ['object', {}]]) {
  cases.push({ name: `changelog-type-${name}`, value: { ...copy(), lastChangelogVersion: value }, want: 'reject' });
}
const outcomes = [];
for (const fixture of cases) {
  let actual = fixture.value, observed = 'accept', infrastructure = false;
  try {
    vm.runInNewContext(body, {
      root, agent, path, action: fixture.action ?? 'verify',
      read(file) {
        if (file === path.join(root, 'selection.json')) return Buffer.from(JSON.stringify({ Destination: root }));
        assert.equal(file, path.join(agent, 'settings.json'), 'only virtual settings and selection reads permitted');
        return Buffer.from(JSON.stringify(actual));
      },
      exclusive(file, bytes) {
        assert.equal(fixture.action, 'install', 'verify must not write settings');
        assert.equal(file, path.join(agent, 'settings.json'));
        actual = JSON.parse(bytes);
      },
      reject(message) { throw Error(`Windows provision refused: ${message}`); },
    }, { timeout: 1000 });
  } catch (error) {
    observed = error.message === refusal ? 'reject' : 'infrastructure';
    infrastructure = observed === 'infrastructure';
  }
  let assertionCode = null;
  try { assert.equal(observed, fixture.want, fixture.name); } catch (error) { assertionCode = error.code; }
  outcomes.push({ name: fixture.name, expected: fixture.want, observed, infrastructure, assertionCode });
}
const passed = outcomes.filter(result => result.assertionCode === null).length;
const report = JSON.stringify({ schema: 'windows-owned-settings-fixtures/v1', total: cases.length, passed, failed: cases.length - passed, outcomes });
assert.ok(Buffer.byteLength(report, 'utf8') <= 4096, 'whole typed fixture report must fit');
fs.writeFileSync(process.argv[3], report, { flag: 'wx', encoding: 'utf8' });
process.exitCode = passed === cases.length ? 0 : 1;
