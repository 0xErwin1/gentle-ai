"""Guest-only syntax and source-binding controls; not hosted CI execution."""
import ast
import pathlib
import subprocess
import sys
import unittest
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[1]
WORKFLOW = pathlib.Path(sys.argv.pop()) if len(sys.argv) == 2 else ROOT / '.github/workflows/shell-linux-first-ci.yml'


class LinuxCIContract(unittest.TestCase):
    def setUp(self):
        self.text = WORKFLOW.read_text()
        self.workflow = yaml.load(self.text, Loader=yaml.BaseLoader)

    def test_upstream_pull_request_uses_exact_public_head(self):
        self.assertIn('pull_request', self.workflow['on'])
        self.assertIn('main', self.workflow['on']['pull_request']['branches'])
        self.assertEqual(self.workflow['permissions'], {})
        job = self.workflow['jobs']['user-vm-laboratory']
        self.assertIn("github.repository == 'Gentleman-Programming/gentle-ai'", job['if'])
        source = job['steps'][0]
        self.assertIn('github.event.pull_request.head.sha', source['env']['SOURCE_SHA'])
        self.assertIn('github.event.pull_request.head.repo.full_name', source['env']['SOURCE_REPOSITORY'])
        self.assertIn('credential.helper=', source['run'])
        self.assertNotIn('actions/checkout', self.text)
        self.assertNotIn('pull_request_target', self.text)

    def test_full_journey_not_historical_or_opening_only_smoke(self):
        self.assertEqual(set(self.workflow['jobs']), {'user-vm-laboratory'})
        self.assertIn('/fixture/harness.py full', self.text)
        self.assertNotIn('/fixture/harness.py smoke', self.text)
        self.assertNotIn('322de52', self.text)
        self.assertNotIn('3.7.0', self.text)

    def test_full_fixture_uses_guarded_internal_kernel_probe(self):
        fixture = ast.parse((ROOT / 'e2e/shell-linux-user-install-guest.py').read_text())
        probes = []
        for node in ast.walk(fixture):
            if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Name) or node.func.id != 'run' or not node.args:
                continue
            args = node.args[0]
            if not isinstance(args, ast.List) or len(args.elts) != 3:
                continue
            binary, command, selector = args.elts
            if isinstance(binary, ast.Name) and binary.id == 'SUPERVISOR' and isinstance(command, ast.Constant) and command.value == 'shell' and isinstance(selector, ast.Constant):
                probes.append(selector.value)
        self.assertIn('internal-check', probes)
        self.assertNotIn('check', probes)

    def test_shell_blocks_parse_without_execution(self):
        for step in self.workflow['jobs']['user-vm-laboratory']['steps']:
            if 'run' not in step:
                continue
            with self.subTest(step=step['name']):
                result = subprocess.run(['/bin/bash', '-n'], input=step['run'], text=True,
                                        capture_output=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr[:1000])


if __name__ == '__main__':
    unittest.main()
