#!/usr/bin/env python3
"""Read-only Oregon preflight. Login and role handling use the shared validation session."""
import argparse
import json
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent / 'aws'))
from core import ConfigurationError, safe_diagnostic, REGION, STATE, TEMPLATE, Session, load_config, private_json

def run_checks(call, expected_account, template, requested_version=None):
    identity = call('sts', 'get-caller-identity')
    expected = f'arn:aws:sts::{expected_account}:assumed-role/course-k-validation-operator/'
    if identity.get('Account') != expected_account or not identity.get('Arn', '').startswith(expected):
        raise ConfigurationError('Unexpected AWS account or operator role; no further checks performed.')
    zones = call('ec2', 'describe-availability-zones', '--filters',
                 'Name=zone-id,Values=usw2-az1,usw2-az2')
    available = {z['ZoneId'] for z in zones.get('AvailabilityZones', []) if z.get('State') == 'available'}
    versions = call('rds', 'describe-db-engine-versions', '--engine', 'mysql')
    mysql84 = sorted({v['EngineVersion'] for v in versions.get('DBEngineVersions', [])
                         if v.get('EngineVersion', '').startswith('8.4.') and v.get('Status') == 'available'},
                        key=lambda value: tuple(int(n) for n in re.findall(r'\d+', value)))
    if not mysql84:
        raise ConfigurationError('No available MySQL 8.4 version returned; do not guess an engine version.')
    chosen = requested_version or mysql84[-1]
    if chosen not in mysql84:
        raise ConfigurationError('Configured MySQL version is not available; do not substitute silently.')
    options = call('rds', 'describe-orderable-db-instance-options', '--engine', 'mysql',
                   '--engine-version', chosen, '--db-instance-class', 'db.t4g.micro')
    gp3 = [o for o in options.get('OrderableDBInstanceOptions', [])
           if o.get('StorageType') == 'gp3' and o.get('MinStorageSize', 999) <= 20 <= o.get('MaxStorageSize', 0)]
    quotas = call('service-quotas', 'list-service-quotas', '--service-code', 'fargate')
    on_demand = [q for q in quotas.get('Quotas', [])
                 if 'on-demand' in q.get('QuotaName', '').lower() and 'vcpu' in q.get('QuotaName', '').lower()]
    validation = call('cloudformation', 'validate-template', '--template-body', 'file://' + str(template))
    return {'region': REGION, 'identity': identity['Arn'],
            'zoneIds': sorted(available), 'requiredZonesAvailable': {'usw2-az1', 'usw2-az2'} <= available,
            'mysql84Versions': mysql84, 'mysqlVersionCandidate': chosen,
            'microGp3_20GiBOrderable': bool(gp3),
            'fargateOnDemandQuotas': [{k: q[k] for k in ('QuotaName', 'QuotaCode', 'Value') if k in q} for q in on_demand],
            'cloudFormationCapabilities': validation.get('Capabilities', []),
            'note': 'Read-only preflight, not a successful deployment or proof of free remaining quota.'}



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--cli')
    args = parser.parse_args()
    config = load_config(args.config)
    session = Session(config, args.cli)
    compact = STATE / config['stack'] / 'preflight-template.json'
    compact.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    # ValidateTemplate accepts at most 51,200 bytes via TemplateBody.
    body = json.dumps(json.loads(TEMPLATE.read_text()), separators=(',', ':'))
    if len(body.encode()) > 51200:
        raise ConfigurationError('Template exceeds direct validation limit.')
    compact.write_text(body)
    compact.chmod(0o600)
    report = run_checks(session.call, config['account'], compact, config['parameters']['MysqlVersion'])
    private_json(STATE / config['stack'] / 'preflight-result.json', report)
    print(json.dumps(report, indent=2))
    if not report['requiredZonesAvailable'] or not report['microGp3_20GiBOrderable']:
        raise ConfigurationError('Required availability condition was not met.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, OSError, KeyError) as error:
        print(safe_diagnostic(error), file=sys.stderr)
        sys.exit(1)
