"""Opt-in one-shot metadata import; no new permissions or live service changes."""
import copy
import json
import sys
from recovery import recovery_template
from production import ref, sub


def backfill_template():
    template = recovery_template()
    template['Parameters'].update({
        'EnableExternalBackfill': {'Type': 'String', 'Default': 'false', 'AllowedValues': ['true', 'false']},
        'ExternalBackfillImage': copy.deepcopy(template['Parameters']['GoImage']),
        'ExternalBackfillItems': {'Type': 'String', 'Default': '', 'MaxLength': 4096},
    })
    template['Parameters']['ExternalBackfillImage'].pop('Description', None)
    template['Conditions']['ExternalBackfill'] = {'Fn::And': [{'Condition': 'DatabaseTools'}, {'Condition': 'RunApplication'}, {'Fn::Equals': [ref('EnableExternalBackfill'), 'true']}]}
    template['Rules']['BackfillRequiresInputs'] = {
        'RuleCondition': {'Fn::Equals': [ref('EnableExternalBackfill'), 'true']},
        'Assertions': [
            {'Assert': {'Fn::Not': [{'Fn::Equals': [ref(name), '']}]}, 'AssertDescription': name + ' required for backfill.'}
            for name in ['ExternalBackfillImage', 'ExternalBackfillItems']
        ] + [{'Assert': {'Fn::Equals': [ref('EnableDatabaseTools'), 'true']}, 'AssertDescription': 'Database tools required.'}],
    }
    # Reuse execution permissions for the Go repository, logs and app DSN.
    # The process itself receives the empty migration task role, not S3 access.
    task = copy.deepcopy(template['Resources']['MigrationTask'])
    task['Condition'] = 'ExternalBackfill'
    task['Properties'].update(Family=sub('${AWS::StackName}-external-backfill'), ExecutionRoleArn={'Fn::GetAtt': ['ApplicationExecutionRole', 'Arn']})
    container = task['Properties']['ContainerDefinitions'][0]
    container.pop('PortMappings', None)
    container.update(Name='externalimport', Image=ref('ExternalBackfillImage'), EntryPoint=['/usr/local/bin/externalimport'], Command=['--confirm-owner-metadata'])
    container['Secrets'] = [{'Name': 'DATABASE_URL', 'ValueFrom': ref('DatabaseUrlParameterArn')}]
    container['Environment'] = [
        {'Name': 'EXPECTED_DB_HOST', 'Value': {'Fn::GetAtt': ['Database', 'Endpoint.Address']}},
        {'Name': 'EXTERNAL_IMPORT_JSON', 'Value': ref('ExternalBackfillItems')},
    ]
    template['Resources']['ExternalBackfillTask'] = task
    template['Outputs']['ExternalBackfillTaskDefinition'] = {'Condition': 'ExternalBackfill', 'Value': ref('ExternalBackfillTask')}
    return template


if __name__ == '__main__':
    json.dump(backfill_template(), sys.stdout, ensure_ascii=False, indent=2)
    print()
