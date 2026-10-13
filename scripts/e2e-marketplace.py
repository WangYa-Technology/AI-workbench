#!/usr/bin/env python3
"""Owned disposable database/processes. Never load .env or reuse a running server."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    admin = os.environ.get('E2E_DATABASE_ADMIN_URL', '')
    url = urllib.parse.urlsplit(admin)
    if url.scheme not in ('postgres', 'postgresql') or url.hostname not in ('localhost', '127.0.0.1') or url.path not in ('/hcai_goal5', '/hcai_e2e_admin') or url.query not in ('', 'sslmode=disable'):
        sys.exit('Set E2E_DATABASE_ADMIN_URL to a dedicated loopback hcai_e2e_admin (or hcai_goal5) database; no default database is used.')
    run = 'hcai_e2e_' + uuid.uuid4().hex
    target = urllib.parse.urlunsplit(url._replace(path='/' + run))
    artifacts = ROOT / 'web/test-results/marketplace-runs' / run
    artifacts.mkdir(parents=True)
    temp = Path(tempfile.mkdtemp(prefix=run + '_'))
    # Explicit allowlist: no inherited provider keys/endpoints, PGOPTIONS or dotenv.
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'TMPDIR', 'CI', 'GOCACHE', 'GOMODCACHE', 'PLAYWRIGHT_BROWSERS_PATH')}
    processes, logs = [], []
    created = False
    ports = []
    result = 1
    def sql(database, statement):
        return subprocess.run(['psql', database, '-X', '-v', 'ON_ERROR_STOP=1', '-qAt'], input=statement, text=True, env=env, check=True, capture_output=True).stdout.strip()
    def command(args):
        child = subprocess.Popen(args, cwd=ROOT, env=env, stdout=build_log, stderr=subprocess.STDOUT, start_new_session=True)
        processes.append(child)
        if child.wait() != 0:
            raise RuntimeError('Build/seed failed; inspect build.log')
        processes.remove(child)
    def launch(args, name, cwd=ROOT):
        log = open(artifacts / (name + '.log'), 'w')
        logs.append(log)
        child = subprocess.Popen(args, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        processes.append(child)
        return child
    def ready(address):
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            if any(p.poll() is not None for p in processes):
                raise RuntimeError('Service exited; inspect service logs')
            try:
                with urllib.request.urlopen(address, timeout=2) as response:
                    if response.status == 200:
                        return
            except (OSError, TimeoutError):
                pass
            time.sleep(.2)
        raise RuntimeError('Service readiness timeout')
    def interrupt(signum, frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    build_log = open(artifacts / 'build.log', 'w')
    evidence = {'run': run, 'database': run, 'schema': 'test_app', 'externalProviders': False, 'startedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    paths = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT).decode().split('\0')
    manifest = {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest() for p in sorted(set(paths)) if p and (ROOT / p).is_file()}
    (artifacts / 'source-sha256.json').write_text(json.dumps(manifest, indent=2) + '\n')
    try:
        # Reserve three distinct ephemeral ports during selection, then release for owned services.
        sockets = [socket.socket() for _ in range(3)]
        for s in sockets:
            s.bind(('127.0.0.1', 0))
        ports = [s.getsockname()[1] for s in sockets]
        api, web, fixture = ports
        for s in sockets:
            s.close()
        env.update(APP_ENV='test', DATABASE_URL=target + ('&' if url.query else '?') + 'search_path=test_app',
                   E2E_SQL_URL=target, E2E_OWNED_DATABASE=run, E2E_MEDIA_ROOT=str(temp / 'media'), MEDIA_ROOT=str(temp / 'media'),
                   HTTP_ADDR=f'127.0.0.1:{api}', WEB_ORIGIN=f'http://127.0.0.1:{web}', VITE_API_TARGET=f'http://127.0.0.1:{api}',
                   E2E_API_PORT=str(api), E2E_WEB_PORT=str(web), E2E_FIXTURE_PORT=str(fixture), E2E_ARTIFACTS=str(artifacts),
                   LOCAL_PROVIDER_ENABLED='true', LOCAL_PROVIDER_SOURCE=str(ROOT / 'web/public/media/home-cinematic.jpg'),
                   EMAIL_DELIVERY_MODE='local_file', MEDIA_STORAGE_ADAPTER='local_file', MEDIA_SCANNER_ADAPTER='local_deterministic',
                   PAYMENT_PROVIDER='stripe', WAFFO_ENABLED='false', STRIPE_ENABLED='true', STRIPE_LIVE_MODE='false',
                   STRIPE_SECRET_KEY='sk_test_payment_drill', STRIPE_WEBHOOK_SECRET='whsec_payment_drill_secret',
                   STRIPE_API_VERSION='2026-02-25.clover', STRIPE_BASE_URL=f'http://127.0.0.1:{fixture}/v1')
        sql(admin, f'CREATE DATABASE {run}')
        created = True
        sql(target, 'CREATE SCHEMA test_app')
        for name, source in [('api', './cmd/api'), ('worker', './cmd/worker'), ('stripefixture', './cmd/stripefixture'), ('seed', './internal/testfixtures/cmd/seed')]:
            command(['go', 'build', '-o', str(temp / name), source])
        command([str(temp / 'seed')])
        launch([str(temp / 'stripefixture'), '-addr', f'127.0.0.1:{fixture}'], 'fixture')
        ready(f'http://127.0.0.1:{fixture}/ready')
        launch([str(temp / 'api')], 'api')
        launch([str(temp / 'worker')], 'worker')
        launch(['node', str(ROOT / 'web/node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', str(web), '--strictPort'], 'web', ROOT / 'web')
        ready(f'http://127.0.0.1:{web}/health')
        child = subprocess.Popen(['node', 'node_modules/@playwright/test/cli.js', 'test', '--config', 'playwright.marketplace.config.ts', *sys.argv[1:]], cwd=ROOT / 'web', env=env, start_new_session=True)
        processes.append(child)
        result = child.wait()
    finally:
        for child in reversed(processes):
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        for child in reversed(processes):
            try:
                child.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=5)
        evidence['processesStopped'] = all(p.poll() is not None for p in processes)
        def port_closed(port):
            with socket.socket() as probe:
                probe.settimeout(1)
                return probe.connect_ex(('127.0.0.1', port)) != 0
        evidence['portsClosed'] = all(port_closed(port) for port in ports)
        try:
            if created:
                sql(admin, f'DROP DATABASE {run}')
                evidence['databaseRemoved'] = sql(admin, f"SELECT count(*) FROM pg_database WHERE datname='{run}'") == '0'
            shutil.rmtree(temp)
            evidence['mediaRemoved'] = not temp.exists()
        except (OSError, subprocess.SubprocessError):
            evidence['cleanupError'] = 'Owned resources remain; inspect the recorded database/run name. No force cleanup attempted.'
            result = 1
        if not evidence['processesStopped'] or not evidence['portsClosed']:
            result = 1
        evidence['exitCode'] = result
        (artifacts / 'isolation.json').write_text(json.dumps(evidence, indent=2) + '\n')
        for log in logs + [build_log]:
            log.close()
        print(f'Marketplace evidence: {artifacts}', flush=True)
    return result


if __name__ == '__main__':
    sys.exit(main())
