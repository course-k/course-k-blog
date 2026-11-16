#!/usr/bin/env python3
"""The production definition after the switch to on-demand delivery.

Readers and the owner reach the blog through CloudFront, the Lambda functions
and the pausing Aurora cluster. This removes what only the Fargate service
used: the service and its task, API Gateway and its VPC link and service
discovery, the MySQL instance (a final snapshot is kept by its deletion
policy), its subnets and security groups, the old probe, and the opt-in
operations built on them (restore exercise, rollback test, metadata backfill).

The earlier generators stay as building blocks: the on-demand side copies the
database tools and settings from the definitions they produce.
"""
import json
import sys

from backfill import backfill_template

# Conditions that only describe the Fargate service and its database.
RETIRED_CONDITIONS = {'RunApplication', 'DatabaseTools', 'MigrationOverride', 'CustomOrigin',
                      'RestoreExercise', 'RestoreTools', 'RestoreRuntimeTest', 'ExternalBackfill'}

# Switches for the Fargate service and the operations built on it.
RETIRED_SWITCHES = {'EnableRuntime', 'EnableDatabaseTools', 'EnableRestoreExercise', 'EnableRestoreTasks',
                    'EnableExternalBackfill'}

# Resources the Fargate service and its database used that have no condition.
RETIRED_RESOURCES = {
    'PublicRouteTable', 'InternetRoute', 'Public1', 'Public1Routes', 'Public2', 'Public2Routes',
    'Private1', 'Private2', 'LinkSg', 'DbSg', 'DbSubnets', 'Database', 'DatabaseParameters',
    'DatabaseStorageAlarm', 'Namespace', 'VpcLink', 'HttpApi', 'Stage', 'PublicDomain', 'PublicMapping',
    'ApplicationDiscovery', 'LinkToApplication', 'ApplicationToDb',
}


def references(value):
    """Logical names and parameters a template fragment refers to."""
    found = set()
    if isinstance(value, dict):
        for key, item in value.items():
            if key == 'Ref' and isinstance(item, str):
                found.add(item)
            elif key == 'Fn::GetAtt':
                target = item[0] if isinstance(item, list) else item.split('.', 1)[0]
                found.add(target)
            elif key == 'Fn::Sub':
                text = item[0] if isinstance(item, list) else item
                found.update(name.split('.', 1)[0] for name in _sub_names(text))
                if isinstance(item, list):
                    found.update(references(item[1]))
            elif key == 'Condition' and isinstance(item, str):
                found.add('condition:' + item)
            else:
                found.update(references(item))
    elif isinstance(value, list):
        for item in value:
            found.update(references(item))
    return found


def _sub_names(text):
    names, start = [], text.find('${')
    while start != -1:
        end = text.find('}', start)
        name = text[start + 2:end]
        if not name.startswith('!') and not name.startswith('AWS::'):
            names.append(name)
        start = text.find('${', end)
    return names


def retire_fargate(template):
    resources = template['Resources']
    gone = {name for name, resource in resources.items()
            if name in RETIRED_RESOURCES or resource.get('Condition') in RETIRED_CONDITIONS}
    for name in gone:
        del resources[name]
    for name in [name for name, output in template['Outputs'].items()
                 if output.get('Condition') in RETIRED_CONDITIONS or references(output) & gone]:
        del template['Outputs'][name]
    for name in RETIRED_CONDITIONS:
        template['Conditions'].pop(name, None)
    template['Rules'] = {name: rule for name, rule in template.get('Rules', {}).items()
                         if not references(rule) & RETIRED_SWITCHES}
    used = references({key: template.get(key, {}) for key in ('Resources', 'Outputs', 'Conditions', 'Rules')})
    template['Parameters'] = {name: value for name, value in template['Parameters'].items()
                              if name in used and name not in RETIRED_SWITCHES}
    if not template['Rules']:
        del template['Rules']
    template['Description'] = 'Course K production blog: CloudFront, Lambda and a pausing Aurora cluster.'
    return template


def current_template():
    return retire_fargate(backfill_template())


if __name__ == '__main__':
    json.dump(current_template(), sys.stdout, ensure_ascii=False, indent=2)
    print()
