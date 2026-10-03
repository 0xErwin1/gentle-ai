#!/usr/bin/python3
"""Credentialless Guest ONLY. Direct evidence is not complete manager/update proof."""
import base64
import hashlib
import http.client
import http.server
import ipaddress
import json
import os
import pathlib
import select
import shutil
import signal
import socket
import ssl
import stat
import struct
import subprocess
import sys
import threading
import time
import urllib.parse
import urllib.request
import fcntl
import termios

START = time.monotonic()
CEILING = 850
WORK = pathlib.Path('/work')
SUPERVISOR = '/usr/local/bin/supervisor'
TESTS = '/usr/local/bin/user-install.test'
FIXTURE = pathlib.Path('/fixture')
MODE = sys.argv[1] if len(sys.argv) == 2 else 'full'
REPORT = {}
COMMAND_FAILURE = None
FAULT = ''
FAULT_SEEN = threading.Event()
FAULT_RELEASE = threading.Event()
CACHE = {}
CACHE_BYTES = 0
CACHE_LOCK = threading.Lock()
REQUEST_ORIGINS = []
PUBLIC_IPS = {}
ALLOW = {'registry.npmjs.org', 'nodejs.org', 'github.com', 'release-assets.githubusercontent.com', 'pi.dev'}
NODE_SHA = '783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8'
PUBLIC_TLS = ssl.create_default_context()


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def remaining():
    value = CEILING - (time.monotonic() - START)
    require(value > 0, 'whole Guest deadline')
    return value


def kernel():
    require(os.getuid() == 1002 and os.uname().machine == 'x86_64', 'real UID1002/linuxamd64 required')
    status = pathlib.Path('/proc/self/status').read_text()
    for key in ['CapInh', 'CapPrm', 'CapEff', 'CapAmb', 'CapBnd']:
        require(f'{key}:\t0000000000000000' in status, 'capability present')
    require('NoNewPrivs:\t1' in status, 'NoNewPrivs missing')
    membership = pathlib.Path('/proc/self/cgroup').read_text()
    require(membership.startswith('0::/'), 'unified actual membership required')
    relative = membership.strip()[3:]
    group = pathlib.Path('/sys/fs/cgroup' + (relative if relative != '/' else ''))
    require(str(group.resolve()) == str(group), 'noncanonical cgroup mapping')
    expected = {'memory.max': '3221225472', 'memory.swap.max': '0', 'cpu.max': '100000 100000', 'pids.max': '64'}
    for name, value in expected.items():
        require((group / name).read_text().strip() == value, 'physical cgroup limit differs')
    require(' - cgroup2 ' in pathlib.Path('/proc/self/mountinfo').read_text(), 'cgroup2 mount missing')
    require(os.statvfs('/').f_flag & os.ST_RDONLY, 'physical Guest root mount must be read-only')
    require(pathlib.Path('/proc/sys/net/ipv4/ip_unprivileged_port_start').read_text().strip() == '0', 'nonprivileged TLS fixture port unavailable')
    REPORT['kernel'] = {'uid': os.getuid(), 'limits': expected}


def run(args, cwd=None, extra=None, timeout=180, good=True, stdout_only=False):
    global COMMAND_FAILURE
    env = {'PATH': '/usr/local/bin:/usr/bin:/bin:/work/personal/prefix/bin', 'HOME': str(WORK / 'home'), 'TMPDIR': str(WORK / 'tmp'),
           'PYTHONDONTWRITEBYTECODE': '1', 'TERM': 'xterm-256color'}
    env.update(extra or {})
    child = subprocess.Popen(args, cwd=cwd or WORK, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, error = child.communicate(timeout=min(timeout, remaining()))
    except subprocess.TimeoutExpired:
        os.killpg(child.pid, signal.SIGKILL)
        child.communicate(timeout=3)
        raise RuntimeError('owned command deadline; raw output withheld')
    raw = out + error
    require(len(raw) < 4096 and b'\0' not in raw, 'whole raw command output withheld: byte/NUL bound')
    text = raw.decode('utf-8', 'strict')
    if (child.returncode == 0) != good:
        COMMAND_FAILURE = {
            'kind': 'tests' if args[0] == TESTS else 'supervisor' if args[0] == SUPERVISOR else 'stock',
            'operation': next((op for op in ['check', 'install', 'recover', 'internal-install', 'internal-run', 'internal-verify'] if op in args), 'stock'),
            'inspect': '--inspect' in args, 'exit': child.returncode, 'expectedSuccess': good,
            'rawBytes': len(raw), 'rawSHA256': hashlib.sha256(raw).hexdigest(),
            'stdoutBase64': base64.b64encode(out).decode('ascii'),
            'stderrBase64': base64.b64encode(error).decode('ascii'),
        }
        raise RuntimeError('command outcome differs; bounded complete failure evidence')
    return out.decode('utf-8', 'strict') if stdout_only else text


def physical_inventory(root):
    info = root.lstat()
    result = [('.', 'metadata', info.st_mode, info.st_uid, info.st_gid, info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)]
    for parent, directories, files in os.walk(root, followlinks=False):
        for name in sorted(directories + files):
            p = pathlib.Path(parent) / name
            info = p.lstat()
            require(info.st_uid == 1002, 'foreign fixture object')
            relative = str(p.relative_to(root))
            result.append((relative, 'metadata', info.st_mode, info.st_uid, info.st_gid, info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns))
            if stat.S_ISLNK(info.st_mode):
                result.append((relative, 'link', os.readlink(p)))
            elif stat.S_ISREG(info.st_mode):
                result.append((relative, 'file', hashlib.sha256(p.read_bytes()).hexdigest()))
            else:
                require(stat.S_ISDIR(info.st_mode), 'nonphysical fixture')
    return hashlib.sha256(json.dumps(sorted(result)).encode()).hexdigest()


def upstream_ip(host):
    require(host in ALLOW, 'unapproved upstream')
    if host not in PUBLIC_IPS:
        url = 'https://1.1.1.1/dns-query?' + urllib.parse.urlencode({'name': host, 'type': 'A'})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(urllib.request.Request(url, headers={'accept': 'application/dns-json'}), timeout=10) as response:
            raw = response.read(8193)
        require(len(raw) <= 8192, 'DNS DATA bound')
        answers = json.loads(raw)['Answer']
        candidates = [entry['data'] for entry in answers if entry['type'] == 1]
        require(candidates, 'no public upstream IPv4')
        PUBLIC_IPS[host] = str(ipaddress.IPv4Address(candidates[0]))
    return PUBLIC_IPS[host]


class DirectTLS(http.client.HTTPSConnection):
    def connect(self):
        sock = socket.create_connection((upstream_ip(self.host), 443), timeout=10)
        self.sock = PUBLIC_TLS.wrap_socket(sock, server_hostname=self.host)


def public_bytes(host, resource):
    global CACHE_BYTES
    key = (host, resource)
    with CACHE_LOCK:
        if key in CACHE:
            return CACHE[key]
    connection = DirectTLS(host, timeout=10)
    try:
        connection.request('GET', resource, headers={'accept-encoding': 'identity', 'user-agent': 'Gentle-Guest-qualification'})
        response = connection.getresponse()
        raw = response.read(67108865)
        require(len(raw) <= 67108864, 'upstream body bound')
        headers = {name.lower(): value for name, value in response.getheaders() if name.lower() in {'content-type', 'location'}}
        if 'location' in headers:
            redirect = urllib.parse.urlsplit(headers['location'])
            require(redirect.scheme == 'https' and redirect.hostname in ALLOW, 'unapproved release redirect')
        with CACHE_LOCK:
            if key not in CACHE:
                require(len(CACHE) < 4096 and CACHE_BYTES + len(raw) <= 536870912, 'upstream aggregate cache bound')
                CACHE[key] = (response.status, headers, raw)
                CACHE_BYTES += len(raw)
            return CACHE[key]
    finally:
        connection.close()


class Mirror(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Never expose signed release URLs or arbitrary stock diagnostics.

    def do_GET(self):
        try:
            host = self.headers.get('Host', '').split(':')[0].lower()
            require(host in ALLOW and len(self.path) <= 4096, 'fixture request origin/path')
            require(len(REQUEST_ORIGINS) < 4096, 'fixture request observation bound')
            REQUEST_ORIGINS.append(host)
            if host == 'nodejs.org':
                require(self.path == '/dist/v24.18.0/node-v24.18.0-linux-x64.tar.gz', 'Node path')
                raw = (FIXTURE / 'node.tgz').read_bytes()
                require(len(raw) == 57224421 and hashlib.sha256(raw).hexdigest() == NODE_SHA, 'independent fixture Node pin')
                if FAULT in {'node-cancel', 'node-cleanup'}:
                    FAULT_SEEN.set()
                    require(FAULT_RELEASE.wait(timeout=min(20, remaining())), 'fault barrier deadline')
                if FAULT in {'node-sri', 'node-cleanup'}:
                    raw = raw[:-1] + bytes([raw[-1] ^ 1])
                status, headers = 200, {}
            elif host == 'pi.dev' and self.path == '/api/latest-version':
                status, headers, raw = 200, {'content-type': 'application/json'}, b'{"version":"1.0.0"}'
            else:
                status, headers, raw = public_bytes(host, self.path)
            self.send_response(status)
            for name, value in headers.items():
                self.send_header(name, value)
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
        except Exception:
            self.send_error(502, 'bounded fixture refusal')


class BoundedServer(http.server.ThreadingHTTPServer):
    slots = threading.BoundedSemaphore(4)
    daemon_threads = True

    def process_request(self, request, address):
        if not self.slots.acquire(timeout=10):
            self.shutdown_request(request)
            return
        super().process_request(request, address)

    def handle_error(self, request, address):
        pass  # Raw unbounded socket tracebacks are not qualification evidence.

    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()


def tls_fixture():
    server = BoundedServer(('127.0.0.1', 443), Mirror)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(FIXTURE / 'server.crt', FIXTURE / 'server.key')
    server.socket = context.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def inspect(target, shared=None):
    args = [SUPERVISOR, 'shell', 'install', '--target', str(target), '--mode', 'shared' if shared else 'separate']
    if shared:
        args += ['--prefix', str(shared[0]), '--agent', str(shared[1])]
    response = run(args + ['--inspect'])
    line = next(line for line in response.splitlines() if line.startswith('Confirmation: '))
    token = line.split(': ', 1)[1]
    require(len(token) == 64 and all(c in '0123456789abcdef' for c in token), 'consent token')
    return args, token


def install(target, shared=None):
    args, token = inspect(target, shared)
    result = run(args + ['--confirm', token], timeout=remaining())
    require('Installed ' in result, 'installation receipt missing')
    manifest = json.loads((target / 'installation.json').read_text())
    require(manifest['Destination'] == str(target), 'manifest destination')
    REPORT.setdefault('installs', []).append({'mode': manifest['Mode'], 'manifestSHA': hashlib.sha256((target / 'installation.json').read_bytes()).hexdigest()})
    return manifest


def acquisition_fault(target, cleanup=False):
    global FAULT
    args, token = inspect(target)
    FAULT_SEEN.clear()
    FAULT_RELEASE.clear()
    FAULT = 'node-cleanup' if cleanup else 'node-cancel'
    env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': str(WORK / 'home'), 'TMPDIR': str(WORK / 'tmp')}
    child = subprocess.Popen(args + ['--confirm', token], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    stage = None
    try:
        require(FAULT_SEEN.wait(timeout=min(20, remaining())), 'actual cold acquisition not observed')
        stages = list(target.parent.glob('.gentle-user-*'))
        require(len(stages) == 1 and stages[0].is_dir() and stages[0].lstat().st_uid == 1002, 'owned acquisition stage differs')
        stage = stages[0]
        if cleanup:
            os.chmod(stage, 0o500)  # Real owned-filesystem cleanup refusal, not a mocked return.
        else:
            child.send_signal(signal.SIGINT)
        FAULT_RELEASE.set()
        out, error = child.communicate(timeout=min(40, remaining()))
        raw = out + error
        require(len(raw) < 4096 and b'\0' not in raw, 'fault command raw output withheld')
        text = raw.decode('utf-8', 'strict')
        require(child.returncode != 0 and not target.exists(), 'fault published or reported success')
        if cleanup:
            require(stage.exists() and str(stage) in text and 'uncertain' in text, 'cleanup denial lost owned evidence locator')
            REPORT['cleanupDenial'] = 'actual changed stage permissions; uncertain evidence retained'
        else:
            require(not stage.exists(), 'canceled acquisition stage not cleaned after worker reap')
            REPORT['acquisitionCancel'] = 'actual in-flight request interrupted, worker waited and unpublished stage removed'
    finally:
        FAULT_RELEASE.set()
        FAULT = ''
        if child.poll() is None:
            child.send_signal(signal.SIGINT)
            try:
                child.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=3)
        if stage is not None and stage.exists():
            os.chmod(stage, 0o700)  # Only this disposable, physically identified Guest-owned stage.


def publication_fault(target, shared):
    args, token = inspect(target, shared)
    changed, stop = threading.Event(), threading.Event()
    node = target / 'runtime/node/bin/node'
    def alter_published_node():
        while not stop.wait(0.002):
            if node.exists():
                info = node.lstat()
                if stat.S_ISREG(info.st_mode) and info.st_uid == 1002:
                    os.chmod(node, 0o600)
                    changed.set()
                return
    observer = threading.Thread(target=alter_published_node, daemon=True)
    observer.start()
    try:
        text = run(args + ['--confirm', token], timeout=remaining(), good=False)
        require(changed.is_set() and target.exists() and str(target) in text and 'uncertain' in text, 'actual post-publication uncertainty not retained')
        REPORT['publicationUncertainty'] = 'actual published Node mode changed during final readback; destination and locator retained'
    finally:
        stop.set()
        observer.join(timeout=3)
        if changed.is_set():
            os.chmod(node, 0o700)


def pty_status(binding, project, command=None, extra=None, installer=None, cancel_installer=False):
    project_before, requests_before = physical_inventory(project), len(REQUEST_ORIGINS)
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 18, 72, 0, 0))

    def terminal():
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    env = {'PATH': '/usr/local/bin:/usr/bin:/bin:/work/personal/prefix/bin', 'HOME': str(WORK / 'home'), 'TMPDIR': str(WORK / 'tmp'), 'TERM': 'xterm-256color'}
    env.update(extra or {})
    selected = command or [str(binding)]
    require(all("'" not in value and '\n' not in value for value in selected), 'unsafe PTY command selection')
    invocation = ' '.join("'" + value + "'" for value in selected)
    script = invocation + "; result=$?; printf '\\nGUEST-STATUS:%d\\n' \"$result\"; read -r finish; exit \"$result\""
    child = subprocess.Popen(['/bin/bash', '--noprofile', '--norc', '-m', '-c', script], cwd=project, env=env, stdin=slave, stdout=slave, stderr=slave, preexec_fn=terminal)
    os.close(slave)
    raw = bytearray()
    deadline = time.monotonic() + min(45, remaining())
    sent = False
    confirmed = False
    stopped = False
    observed_cwd = ''
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                try:
                    part = os.read(master, 4096)
                except OSError:
                    break
                if not part:
                    break
                raw.extend(part)
                require(len(raw) <= 65536, 'whole PTY capture bound')
                if not sent and (installer is None or b'Gentle Shell Linux user installer' in raw):
                    if installer is not None:
                        os.write(master, b'\x1b' if cancel_installer else (str(installer) + '\r').encode())
                        confirmed = cancel_installer
                    else:
                        os.write(master, b'\r/gentle:status\r')
                    sent = True
                if installer is not None and not confirmed and b'Confirm this physical selection' in raw:
                    os.write(master, b'y')
                    confirmed = True
                if installer is None and b'el Gentleman package is active.' in raw and not stopped:
                    foreground = os.tcgetpgrp(master)
                    observed_cwd = os.readlink(f'/proc/{foreground}/cwd')
                    require(observed_cwd == str(project), 'actual foreground caller CWD differs')
                    os.write(master, b'\x03\x03')
                    stopped = True
                if b'GUEST-STATUS:' in raw:
                    require(b'GUEST-STATUS:0' in raw, 'interactive child failed; evidence retained')
                    require(os.tcgetpgrp(master) == child.pid, 'real caller terminal foreground not restored')
                    os.write(master, b'finish\n')
                    break
        if installer is None:
            require(b'el Gentleman package is active.' in raw, 'actual Gentle command registration not observed')
            require(b'Gentle Shell Linux user installer' not in raw, 'ordinary launch opened installer')
        else:
            require(confirmed and b'GUEST-STATUS:0' in raw, 'installer TUI did not settle successfully')
            REPORT['installerCancel' if cancel_installer else 'installerTUI'] = 'actual PTY idle cancellation' if cancel_installer else 'actual PTY physical review, typed confirmation and settled idempotent install'
        child.wait(timeout=5)
        require(physical_inventory(project) == project_before, 'fixture blank caller project changed during launch')
        if command is None:
            require(set(REQUEST_ORIGINS[requests_before:]) <= {'pi.dev'}, 'ordinary launch requested installer/package artifacts')
            REPORT['ordinaryStartup'] = 'blank caller project preserved; fixture-origin installer/package requests absent (not whole-network attestation)'
        REPORT.setdefault('pty', []).append({'binding': binding.name, 'rawBytes': len(raw), 'rawSHA256': hashlib.sha256(raw).hexdigest(), 'nulBytes': raw.count(0), 'GentleRegistered': installer is None, 'foregroundCWD': observed_cwd, 'callerForegroundRestored': True})
    finally:
        if child.poll() is None:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait(timeout=3)
        os.close(master)


def alan_npm_backend_probe():
    """Reuse Alan PR1703's persisted npm pin; no browser or live-prefix changes."""
    root = WORK / 'reuse-npm-backend'
    root.mkdir(mode=0o700)
    for name in ['runtime', 'home', 'tmp', 'config', 'state', 'agent', 'project', 'tooling']:
        (root / name).mkdir(mode=0o700)
    for name in ['user.npmrc', 'global.npmrc']:
        (root / 'config' / name).write_bytes(b'')
    run(['/bin/sh', '/work/src/scripts/bootstrap-gentle-shell-private-node.sh', '--destination', str(root / 'runtime/node'), '--node-archive', str(FIXTURE / 'node.tgz')], timeout=180)
    node = root / 'runtime/node/bin/node'
    npm = root / 'runtime/node/lib/node_modules/npm'
    tool = root / 'tooling'
    version = '11.19.0'  # Alan's installer-preflight.mjs at 5ed499ade6c93733990e0298052df477428a90be.
    integrity = 'sha512-SDd/hHg3KqHE5Ht2NHWxNYNtqCQ2pXAPLl6OtQhPyED5PHsRfrOtO199MZTIG2cQoQ1ZRI9t28shrD+2cr3AAw=='
    (tool / 'package.json').write_text(json.dumps({'name': 'gentle-owned-npm-probe', 'version': '1.0.0', 'private': True, 'dependencies': {'npm': version}}))
    env = {'HOME': str(root / 'home'), 'TMPDIR': str(root / 'tmp'), 'PATH': str(node.parent) + ':/usr/bin:/bin',
           'NODE_USE_SYSTEM_CA': '1', 'NPM_CONFIG_USERCONFIG': str(root / 'config/user.npmrc'),
           'NPM_CONFIG_GLOBALCONFIG': str(root / 'config/global.npmrc'), 'NPM_CONFIG_CACHE': str(root / 'tool-cache'),
           'NPM_CONFIG_PREFIX': str(tool), 'NPM_CONFIG_IGNORE_SCRIPTS': 'true', 'npm_config_ignore_scripts': 'true'}
    flags = ['--ignore-scripts', '--engine-strict', '--no-audit', '--no-fund', '--min-release-age=0', '--registry=https://registry.npmjs.org/', '--loglevel=error']
    run([str(node), str(npm / 'bin/npm-cli.js'), 'install', '--package-lock-only'] + flags, cwd=tool, extra=env)
    lock_bytes = (tool / 'package-lock.json').read_bytes()
    require(len(lock_bytes) <= 33554432, 'npm probe lock bound')
    lock = json.loads(lock_bytes)
    entry = lock.get('packages', {}).get('node_modules/npm', {})
    require(lock.get('lockfileVersion') == 3 and lock['packages']['']['dependencies'] == {'npm': version}, 'npm probe seed differs')
    require(entry.get('version') == version and entry.get('integrity') == integrity and entry.get('resolved') == 'https://registry.npmjs.org/npm/-/npm-11.19.0.tgz', 'independent npm probe pin differs')
    require(all(key in {'', 'node_modules/npm'} or (key.startswith('node_modules/npm/node_modules/') and record.get('inBundle') is True) for key, record in lock['packages'].items()), 'npm probe has unpinned external dependencies')
    run([str(node), str(npm / 'bin/npm-cli.js'), 'ci'] + flags, cwd=tool, extra=env)
    require((tool / 'package-lock.json').read_bytes() == lock_bytes, 'npm probe lock changed')
    acquired = tool / 'node_modules/npm'
    require(json.loads((acquired / 'package.json').read_bytes())['version'] == version, 'npm probe package identity')
    require(run([str(node), str(acquired / 'bin/npm-cli.js'), '--version'], extra=env).strip() == version, 'acquired stock npm did not execute')
    # Replace only this disposable, unpublished probe runtime; retain its old npm.
    npm.rename(root / 'stock-bootstrap-npm')
    shutil.copytree(acquired, npm, symlinks=False)
    for name in ['complete-generated-lock-sri.mjs', 'normalize-private-optional-platform-closure.mjs']:
        shutil.copyfile(WORK / 'src/scripts' / name, root / name)
        os.chmod(root / name, 0o400)
    shutil.copyfile(WORK / 'src/scripts/provision-gentle-shell-private-global.mjs', root / 'provision.mjs')
    os.chmod(root / 'provision.mjs', 0o400)
    prefix = root / 'prefix'
    # Exact existing five-root integrity, byte, dependency and native checks; no normalization of live trees.
    run([str(node), str(root / 'provision.mjs'), str(root), str(prefix), str(root / 'agent'), str(prefix), str(root), 'separate', 'install'], extra=env, timeout=remaining())
    graph = json.loads((root / 'state/global-graph.json').read_bytes())
    require(graph and not any(item['name'] == '@esbuild/aix-ppc64' for item in graph), 'nonapplicable AIX package survived full authenticated readback')
    cli = prefix / 'lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js'
    require(run([str(node), str(cli), '--version'], extra={**env, 'PI_CODING_AGENT_DIR': str(root / 'agent')}).strip() == '1.0.0', 'probe stock Pi version differs')
    REPORT['alanBackendProbe'] = {'npm': version, 'authenticatedGlobalPackages': len(graph), 'pi': '1.0.0', 'functionalReady': False}


def main():
    global FAULT
    require(MODE in {'probe', 'direct', 'full'}, 'explicit qualification mode')
    if MODE == 'probe':
        kernel()
        print(json.dumps({'physicalWorker': REPORT['kernel'], 'functionalReady': False}, sort_keys=True))
        return
    if MODE == 'full':
        require(pathlib.Path('/run/user/1002/systemd/private').is_socket() and pathlib.Path('/run/user/1002/bus').is_socket(), 'STOP: pre-existing delegated manager and bus unavailable')
    kernel()
    require(not any(key in os.environ for key in ['GITHUB_TOKEN', 'NPM_TOKEN', 'AWS_ACCESS_KEY_ID', 'SSH_AUTH_SOCK']), 'credentials present')
    for name in ['home', 'tmp', 'project', 'personal', 'parents']:
        (WORK / name).mkdir(mode=0o700, parents=True, exist_ok=False)
    server = tls_fixture()
    try:
        run([SUPERVISOR, 'shell', 'check'])
        tests = run([TESTS, '-test.run=^TestUser', '-test.timeout=90s'], timeout=100)
        require('PASS' in tests, 'focused Go controls')
        personal = WORK / 'personal'
        (personal / 'sentinel').write_bytes(b'personal Pi must remain unchanged\n')
        before = physical_inventory(personal)
        alan_npm_backend_probe()
        require(physical_inventory(personal) == before, 'Alan backend probe changed personal Pi')
        FAULT = 'node-sri'
        bad = WORK / 'parents/refused-node'
        args, token = inspect(bad)
        run(args + ['--confirm', token], good=False)
        require(not bad.exists() and physical_inventory(personal) == before, 'bad acquisition affected personal Pi')
        FAULT = ''
        acquisition_fault(WORK / 'parents/canceled-node')
        acquisition_fault(WORK / 'parents/cleanup-node', cleanup=True)
        require(physical_inventory(personal) == before, 'acquisition faults changed personal Pi')
        target = WORK / 'parents/separate'
        manifest = install(target)
        require(physical_inventory(personal) == before, 'separate touched personal Pi')
        install(target)  # Physical idempotent retry uses actual global readback.
        shutil.copytree(pathlib.Path(manifest['Prefix']), personal / 'prefix', symlinks=True)
        personal_agent = WORK / 'home/.pi/agent'
        personal_agent.mkdir(mode=0o700, parents=True)
        (personal_agent / 'settings.json').write_text('{"theme":"personal-preserved","packages":[]}\n')
        personal_before, personal_agent_before = physical_inventory(personal), physical_inventory(personal_agent)
        pty_status(pathlib.Path(SUPERVISOR), WORK / 'project', [SUPERVISOR, 'shell', 'install'], installer=target)
        pty_status(pathlib.Path(SUPERVISOR), WORK / 'project', [SUPERVISOR, 'shell', 'install'], installer='', cancel_installer=True)
        os.chmod(target.parent, 0o770)
        try:
            run([SUPERVISOR, 'shell', 'install', '--target', str(target), '--mode', 'separate', '--inspect'], good=False)
            for binding in ['gentle-shell', 'pi']:
                run([str(target / 'bin' / binding), '--version'], good=False)
        finally:
            os.chmod(target.parent, 0o700)
        REPORT['existingParentGuard'] = 'occupied installation refused under changed nonprivate parent'
        run(['git', '-c', 'core.hooksPath=/dev/null', 'init', '-q', str(WORK / 'project')])
        run(['git', '-c', 'core.hooksPath=/dev/null', '-c', 'user.name=Guest Fixture', '-c', 'user.email=guest@invalid.local', 'commit', '--allow-empty', '-m', 'fixture'], cwd=WORK / 'project')
        native = pathlib.Path(manifest['Prefix']) / 'lib/node_modules/gentle-pi/.gentle-ai/v4.0.0/gentle-ai'
        require('4.0.0' in run([str(native), '--version']), 'authenticated native v4 did not execute')
        backend = json.loads(run([str(native), 'review', 'status', '--contract', 'gentle-ai.review-integration/v2', '--cwd', str(WORK / 'project'), '--projection', 'workspace', '--next-transition'], cwd=WORK / 'project', timeout=35, stdout_only=True))
        require(backend.get('contract') == 'gentle-ai.review-integration/v2' and backend.get('operation') == 'review.status' and backend.get('applicability') == 'current_target', 'native read-only negotiated status identity differs')
        require(backend.get('schema') in {'gentle-ai.review-integration.status/v' + str(v) for v in [3, 5, 6, 7, 8, 9]}, 'unsupported native status schema')
        REPORT['nativeBackend'] = backend['schema']
        pty_status(target / 'bin/gentle-shell', WORK / 'project')
        pty_status(target / 'bin/pi', WORK / 'project')
        prefix, agent = pathlib.Path(manifest['Prefix']), pathlib.Path(manifest['Agent'])
        roots_before = [(p.stat().st_dev, p.stat().st_ino) for p in [prefix, agent]]
        bindings_before = {name: (target / 'bin' / name).read_bytes() for name in ['pi', 'gentle-shell']}
        run([str(target / 'runtime/node/bin/node'), str(target / 'provision.mjs'), str(target), str(prefix), str(agent), str(prefix), str(target), 'separate', 'prepare-prior'], timeout=remaining())
        require('0.99.2' in run([str(target / 'bin/pi'), '--version']), 'authenticated prior did not launch')
        nested = prefix / 'lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/package.json'
        require(json.loads(nested.read_text())['version'] == '0.99.2', 'actual prior nested TUI placement differs')
        run([str(target / 'bin/pi'), 'update', '--self'], cwd=WORK / 'project', timeout=180)
        require('1.0.0' in run([str(target / 'bin/pi'), '--version']), 'actual stock prior-to-next transition absent')
        require(roots_before == [(p.stat().st_dev, p.stat().st_ino) for p in [prefix, agent]], 'update replaced physical selected roots')
        require(all((target / 'bin' / name).read_bytes() == data for name, data in bindings_before.items()), 'update changed owned command bindings')
        REPORT['trueUpgrade'] = 'actual published 0.99.2 to 1.0.0 stock updater; TEST-only Pi API selection, not public latest authority'
        run([str(target / 'bin/pi'), 'update', '--self', '--force'], cwd=WORK / 'project', timeout=180)
        run([str(target / 'bin/pi'), '--version'], cwd=WORK / 'project')
        REPORT['stockForce'] = 'actual separate same-version reinstall after true upgrade'
        coding_metadata = prefix / 'lib/node_modules/@earendil-works/pi-coding-agent/package.json'
        expected_metadata = coding_metadata.read_bytes()
        corrupted = json.loads(expected_metadata)
        corrupted['version'] = '9.9.9'
        coding_metadata.write_text(json.dumps(corrupted))
        run([str(target / 'bin/pi'), '--version'], good=False)
        upgrade_recovery = run([SUPERVISOR, 'shell', 'recover', str(target), 'inspect'])
        upgrade_token = next(line.split(': ', 1)[1] for line in upgrade_recovery.splitlines() if line.startswith('Recovery confirmation: '))
        run([SUPERVISOR, 'shell', 'recover', str(target), upgrade_token], timeout=120)
        require(coding_metadata.read_bytes() == expected_metadata, 'separate upgrade preimages did not restore known bytes')
        require('1.0.0' in run([str(target / 'bin/pi'), '--version']), 'restored known cohort did not retry successfully')
        REPORT['upgradeRecovery'] = 'actual unknown cohort refused, full saved trees restored and authenticated retry observed'
        shared_prefix, shared_agent = WORK / 'shared-prefix', WORK / 'shared-agent'
        shutil.copytree(manifest['Prefix'], shared_prefix, symlinks=True)
        shared_agent.mkdir(mode=0o700)
        (shared_agent / 'settings.json').write_text('{}\n')
        os.chmod(shared_prefix, 0o700)
        os.chmod(shared_agent / 'settings.json', 0o600)
        shared = WORK / 'parents/shared'
        args, token = inspect(shared, (shared_prefix, shared_agent))
        (shared_agent / 'settings.json').write_text('{"theme":"dark"}\n')
        run(args + ['--confirm', token], good=False)
        shared_manifest = install(shared, (shared_prefix, shared_agent))
        require(shared_manifest['Prefix'] == str(shared_prefix) and shared_manifest['Agent'] == str(shared_agent), 'shared physical binding selection')
        pty_status(shared / 'bin/gentle-shell', WORK / 'project')
        pty_status(shared / 'bin/pi', WORK / 'project')
        REPORT['manager'] = 'UNQUALIFIED: real externally provided delegated manager required'
        if MODE == 'full':
            runtime = pathlib.Path('/run/user/1002')
            require(stat.S_ISSOCK((runtime / 'systemd/private').stat().st_mode), 'pre-existing user manager socket unavailable')
            manager_env = {'GENTLE_USER_MANAGER_GUEST': 'approved', 'GENTLE_USER_SUPERVISOR': SUPERVISOR,
                           'GENTLE_USER_INSTALLED': str(shared), 'GENTLE_USER_PROJECT': str(WORK / 'project'),
                           'XDG_RUNTIME_DIR': str(runtime), 'DBUS_SESSION_BUS_ADDRESS': 'unix:path=' + str(runtime / 'bus')}
            check = run([TESTS, '-test.run=^TestUserDelegatedManagerIntegration$', '-test.timeout=25s'], extra=manager_env, timeout=30)
            require('PASS' in check, 'real delegated controller check')
            pty_status(pathlib.Path(TESTS), WORK / 'project', [TESTS, '-test.run=^TestUserDelegatedManagerLaunch$', '-test.timeout=70s'], manager_env)
            REPORT['manager'] = 'actual manager/controller/PTY/stop-subtree readback observed'
        recovery = run([SUPERVISOR, 'shell', 'recover', str(shared), 'inspect'])
        recovery_token = next(line.split(': ', 1)[1] for line in recovery.splitlines() if line.startswith('Recovery confirmation: '))
        (shared_agent / 'settings.json').write_text('{"theme":"after-inspect"}\n')
        run([SUPERVISOR, 'shell', 'recover', str(shared), recovery_token], good=False)
        recovery = run([SUPERVISOR, 'shell', 'recover', str(shared), 'inspect'])
        recovery_token = next(line.split(': ', 1)[1] for line in recovery.splitlines() if line.startswith('Recovery confirmation: '))
        run([SUPERVISOR, 'shell', 'recover', str(shared), recovery_token], timeout=120)
        require(json.loads((shared_agent / 'settings.json').read_text()) == {'theme': 'dark'}, 'shared exact settings restoration')
        run([SUPERVISOR, 'shell', 'recover', str(shared), 'inspect'])
        REPORT['sharedRecovery'] = 'actual fresh-confirm restoration with retained quarantine'
        publication_fault(WORK / 'parents/publication-fault', (shared_prefix, shared_agent))
        REPORT['recoveryFaultMatrix'] = 'actual acquisition cancellation, cleanup denial, stale recovery consent and post-publication uncertainty controls'
        require(physical_inventory(personal) == personal_before and physical_inventory(personal_agent) == personal_agent_before, 'actual personalized published Pi prefix/configuration changed')
        REPORT['personalizedPi'] = 'actual authenticated published prefix, default agent settings and metadata preserved'
        REPORT['functionalReady'] = MODE == 'full'
        if MODE == 'full':
            format_data = WORK / 'gofmt.data'
            require(format_data.is_file() and format_data.stat().st_size == 0, 'whole source formatting not yet qualified')
            require(all(key in REPORT for key in ['manager', 'nativeBackend', 'trueUpgrade', 'upgradeRecovery', 'installerTUI', 'installerCancel', 'publicationUncertainty', 'sharedRecovery', 'recoveryFaultMatrix']), 'full observed qualification record incomplete')
    finally:
        server.shutdown()
        server.server_close()
    output = (json.dumps(REPORT, sort_keys=True, separators=(',', ':')) + '\n').encode()
    require(len(output) < 4096, 'entire raw Guest report withheld above bound')
    sys.stdout.buffer.write(output)


try:
    main()
except Exception as error:
    message = {'functionalReady': False, 'errorType': type(error).__name__, 'reason': str(error)[:240], 'rawStockOutput': 'withheld'}
    if 'alanBackendProbe' in REPORT:
        message['alanBackendProbe'] = REPORT['alanBackendProbe']
    if COMMAND_FAILURE is not None:
        message.update(commandFailure=COMMAND_FAILURE, rawStockOutput='complete-base64')
    output = (json.dumps(message, sort_keys=True) + '\n').encode()
    if len(output) >= 4096:
        message = {'functionalReady': False, 'reason': 'entire command failure output withheld at original Guest bound', 'rawStockOutput': 'withheld', 'bytes': len(output), 'sha256': hashlib.sha256(output).hexdigest()}
        output = (json.dumps(message, sort_keys=True) + '\n').encode()
    if len(output) < 4096:
        sys.stdout.buffer.write(output)
    sys.exit(1)
