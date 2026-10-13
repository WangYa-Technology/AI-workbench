#!/usr/bin/env python3
"""Run fixture-only acceptance; never reads dotenv or starts real provider tools.

Requires dependencies already installed. Evidence is private and synthetic.
Database-backed restart tests are deliberately a separate, explicitly targeted run.
"""
import datetime
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile


def main():
    if len(sys.argv) != 1:
        print('Usage: python3 scripts/facility-acceptance-local.py', file=sys.stderr)
        return 2
    root = pathlib.Path(__file__).resolve().parent.parent
    os.umask(0o077)
    output = pathlib.Path(tempfile.mkdtemp(prefix='hcai-facility-local-'))
    env = {k: v for k, v in os.environ.items() if k in (
        'PATH', 'HOME', 'TMPDIR', 'GOCACHE', 'GOMODCACHE', 'GOPATH', 'SDKROOT',
        'DEVELOPER_DIR', 'SYSTEMROOT', 'TEMP', 'TMP')}
    env.update(GOPROXY='off', GOTOOLCHAIN='local', GOENV='off', GOSUMDB='off',
               APP_ENV='test', HCAI_REQUIRE_INTEGRATION_TESTS='1',
               TEST_DATABASE_URL='postgres://127.0.0.1:1/unavailable?connect_timeout=1')
    # Use an installed toolchain directly. The go shim may otherwise attempt
    # network checksum verification even when the toolchain is already cached.
    try:
        info = json.loads(subprocess.check_output(
            ['go', 'env', '-json', 'GOVERSION', 'GOMODCACHE', 'GOOS', 'GOARCH'],
            cwd=root, env=env, stderr=subprocess.DEVNULL, timeout=15))
        required = re.search(r'^go ([0-9.]+)$', (root / 'go.mod').read_text(), re.M)[1]
        version = lambda value: tuple(int(x) for x in value.removeprefix('go').split('.'))
        go = 'go'
        if version(info['GOVERSION']) < version(required):
            go = str(pathlib.Path(info['GOMODCACHE']) / (
                'golang.org/toolchain@v0.0.1-go' + required + '.' +
                info['GOOS'] + '-' + info['GOARCH']) / 'bin/go')
            if not pathlib.Path(go).is_file():
                raise ValueError('toolchain_missing')
    except (OSError, ValueError, TypeError, subprocess.SubprocessError):
        print(json.dumps({'status': 'blocked', 'errorCode': 'installed_toolchain_required',
                          'externalAcceptancePassed': False}))
        return 1
    payment_selection = ('^Test(StripeAcceptance.*|StripePayout.*|'
                         'StripeTransferReversal.*|StripeRefundReadBoundary|'
                         'StripeSharedPostCannotBeTransportReplayed|'
                         'StripeLostResponseOnReusedConnectionDoesNotReplay|'
                         'PayoutCapabilityRequiresImplementation|WaffoRuntime.*|'
                         'WaffoRefundRejectsUnboundResponses|'
                         'MinimizeWaffoEventUsesDeliveryIDAndExactAmount)$')
    connector = root / 'services/waffo-connector'
    suites = [
        ('offline-media-mail', root, [go, 'test', '-json', '-race', '-count=1',
            '-timeout=5m', './cmd/facilitycheck', './internal/platform/media',
            './internal/platform/mailer', './internal/platform/config']),
        ('payment-contracts', root, [go, 'test', '-json', '-race', '-count=1',
            '-timeout=8m', './internal/payments', '-run', payment_selection]),
        ('waffo-connector', connector,
         ['node', '--test'] + [p.name for p in sorted(connector.glob('*.test.mjs'))]),
    ]
    tracked = subprocess.check_output(['git', 'ls-files', '--cached', '--others',
                                      '--exclude-standard', '-z'], cwd=root)
    manifest = {}
    for name in tracked.decode().split('\0'):
        p = root / name
        if name and p.is_file():
            manifest[name] = hashlib.sha256(p.read_bytes()).hexdigest()
    (output / 'source-manifest.json').write_text(json.dumps(manifest, indent=2))
    results = {'scope': 'local_synthetic_contracts_only',
               'externalAcceptancePassed': False, 'suites': []}
    failed = False
    for name, cwd, command in suites:
        record = {'name': name, 'command': command,
                  'startedUTC': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        logpath = output / (name + '.log')
        try:
            with logpath.open('w') as log:
                proc = subprocess.run(command, cwd=cwd, env=env, stdout=log,
                                      stderr=subprocess.STDOUT, timeout=600)
            record['exitCode'] = proc.returncode
            failed |= proc.returncode != 0
            if name != 'waffo-connector':
                events = []
                for line in logpath.read_text().splitlines():
                    if line.startswith('{'):
                        events.append(json.loads(line))
                passed = [e for e in events if e.get('Action') == 'pass' and
                          'Test' in e and '/' not in e['Test']]
                skipped = [e for e in events if e.get('Action') == 'skip']
                record.update(topLevelPassed=len(passed), skipped=len(skipped))
                if not passed or skipped:
                    failed = True
                    record['evidenceError'] = 'missing_tests_or_skipped_tests'
        except (OSError, subprocess.TimeoutExpired):
            record['exitCode'] = -1
            record['evidenceError'] = 'local_runner_failed'
            failed = True
        record['finishedUTC'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        results['suites'].append(record)
        (output / 'results.json').write_text(json.dumps(results, indent=2))
    results['status'] = 'failed' if failed else 'local_contracts_passed'
    (output / 'results.json').write_text(json.dumps(results, indent=2))
    print(json.dumps({'status': results['status'], 'evidenceDirectory': str(output),
                      'externalAcceptancePassed': False}))
    return 1 if failed else 0


if __name__ == '__main__':
    sys.exit(main())
