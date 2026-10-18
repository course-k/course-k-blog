#!/usr/bin/env python3
"""Read-only AWS checks for the approved Oregon validation environment.

The CLI handles credentials. This script never prints or persists credentials.
It makes no resource-creation, permission-change, or deployment calls.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REGION = 'us-west-2'


def cli_environment():
    env = os.environ.copy()
    for name in ('AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_SESSION_TOKEN', 'AWS_SECURITY_TOKEN', 'AWS_PROFILE', 'AWS_DEFAULT_PROFILE'):
        env.pop(name, None)
    directory = ROOT / 'config' / 'aws-validation'
    env.update(AWS_CONFIG_FILE=str(directory / 'config'),
               AWS_SHARED_CREDENTIALS_FILE=str(directory / 'credentials'),
               AWS_LOGIN_CACHE_DIRECTORY=str(directory / 'login-cache'), AWS_PAGER='')
    return env


def run_checks(call, expected_account, template):
    identity = call('sts', 'get-caller-identity')
    expected = f'arn:aws:sts::{expected_account}:assumed-role/course-k-validation-operator/'
    if identity.get('Account') != expected_account or not identity.get('Arn', '').startswith(expected):
        raise ValueError('Unexpected AWS account or operator role; no further checks performed.')
    zones = call('ec2', 'describe-availability-zones', '--filters',
                 'Name=zone-id,Values=usw2-az1,usw2-az2')
    available = {z['ZoneId'] for z in zones.get('AvailabilityZones', []) if z.get('State') == 'available'}
    versions = call('rds', 'describe-db-engine-versions', '--engine', 'postgres')
    postgres17 = sorted({v['EngineVersion'] for v in versions.get('DBEngineVersions', [])
                         if v.get('EngineVersion', '').startswith('17.') and v.get('Status') == 'available'},
                        key=lambda value: tuple(int(n) for n in re.findall(r'\d+', value)))
    if not postgres17:
        raise ValueError('No available PostgreSQL 17 version returned; do not guess an engine version.')
    chosen = postgres17[-1]
    options = call('rds', 'describe-orderable-db-instance-options', '--engine', 'postgres',
                   '--engine-version', chosen, '--db-instance-class', 'db.t4g.micro')
    gp3 = [o for o in options.get('OrderableDBInstanceOptions', [])
           if o.get('StorageType') == 'gp3' and o.get('MinStorageSize', 999) <= 20 <= o.get('MaxStorageSize', 0)]
    quotas = call('service-quotas', 'list-service-quotas', '--service-code', 'fargate')
    on_demand = [q for q in quotas.get('Quotas', [])
                 if 'on-demand' in q.get('QuotaName', '').lower() and 'vcpu' in q.get('QuotaName', '').lower()]
    validation = call('cloudformation', 'validate-template', '--template-body', 'file://' + str(template))
    return {'region': REGION, 'identity': identity['Arn'],
            'zoneIds': sorted(available), 'requiredZonesAvailable': {'usw2-az1', 'usw2-az2'} <= available,
            'postgres17Versions': postgres17, 'postgresVersionCandidate': chosen,
            'microGp3_20GiBOrderable': bool(gp3),
            'fargateOnDemandQuotas': [{k: q[k] for k in ('QuotaName', 'QuotaCode', 'Value') if k in q} for q in on_demand],
            'cloudFormationCapabilities': validation.get('Capabilities', []),
            'note': 'Read-only preflight, not a successful deployment or proof of free remaining quota.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--expected-account', required=True)
    parser.add_argument('--source-profile', default='course-k-blog')
    parser.add_argument('--cli', default=os.getenv('AWS_VALIDATION_CLI') or shutil.which('aws'))
    args = parser.parse_args()
    if not re.fullmatch(r'\d{12}', args.expected_account) or not args.cli:
        parser.error('A 12-digit expected account and AWS CLI binary are required.')
    env = cli_environment()
    version = subprocess.run([args.cli, '--version'], env=env, capture_output=True, text=True, check=True)
    match = re.search(r'aws-cli/(\d+)\.(\d+)\.(\d+)', version.stdout)
    if not match or tuple(map(int, match.groups())) < (2, 32, 0):
        raise ValueError('AWS CLI 2.32.0+ is required for the dedicated browser-login profile.')
    directory = ROOT / 'config' / 'aws-validation'
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    source = json.loads((ROOT / 'infra/review/two-task-stack.template.json').read_text())
    compact = directory / 'preflight-template.json'
    compact.write_text(json.dumps(source, separators=(',', ':')))
    compact.chmod(0o600)

    # Assume directly and keep short-lived credentials in this process only.
    # Role profiles would otherwise create a cache in the global ~/.aws/cli.
    role = subprocess.run([args.cli, 'sts', 'assume-role', '--role-arn',
                           f'arn:aws:iam::{args.expected_account}:role/course-k-validation-operator',
                           '--role-session-name', 'blog-validation', '--profile', args.source_profile,
                           '--region', REGION, '--output', 'json', '--no-cli-pager'],
                          env=env, capture_output=True, text=True)
    if role.returncode:
        raise RuntimeError('AssumeRole failed: ' + role.stderr.strip())
    credentials = json.loads(role.stdout)['Credentials']
    env.update(AWS_ACCESS_KEY_ID=credentials['AccessKeyId'],
               AWS_SECRET_ACCESS_KEY=credentials['SecretAccessKey'],
               AWS_SESSION_TOKEN=credentials['SessionToken'])

    def call(*operation):
        response = subprocess.run([args.cli, *operation, '--region', REGION,
                                   '--output', 'json', '--no-cli-pager', '--cli-connect-timeout', '10',
                                   '--cli-read-timeout', '30'], env=env, capture_output=True, text=True)
        if response.returncode:
            # These operations neither request nor return secret values. Never use --debug here.
            raise RuntimeError(f'{operation[0]} {operation[1]} failed: {response.stderr.strip()}')
        return json.loads(response.stdout)

    report = run_checks(call, args.expected_account, compact)
    destination = directory / 'preflight-result.json'
    destination.write_text(json.dumps(report, indent=2) + '\n')
    destination.chmod(0o600)
    print(json.dumps(report, indent=2))
    if not report['requiredZonesAvailable'] or not report['microGp3_20GiBOrderable']:
        raise ValueError('A required availability condition was not met; review before deployment.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
