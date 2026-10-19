#!/usr/bin/env python3
"""Render opt-in, disposable recovery resources without changing the live topology."""
import copy
import json
import sys

from production import production_template, ref, sub


def renamed(value, names):
    if isinstance(value, dict):
        return {key: renamed(item, names) for key, item in value.items()}
    if isinstance(value, list):
        return [renamed(item, names) for item in value]
    if isinstance(value, str):
        if value in names:
            return names[value]
        for source, target in names.items():
            value = value.replace('${' + source + '}', '${' + target + '}')
            value = value.replace('${' + source + '.', '${' + target + '.')
    return value


def recovery_template():
    template = production_template()
    resources, params = template['Resources'], template['Parameters']
    params.update({name: {'Type': 'String', 'Default': 'false', 'AllowedValues': ['true', 'false']}
                   for name in ['EnableRestoreExercise', 'EnableRestoreTasks']})
    params['RestoreCheckImage'] = copy.deepcopy(params['BootstrapImage'])
    params['RestoreBackupId'] = {'Type': 'String', 'Default': '', 'AllowedPattern': '^$|^[A-Za-z0-9_-]{43}$'}
    names = {'Database': 'RestoreDatabase'}
    for name in ['DatabaseUrlParameterArn', 'MigrationUrlParameterArn', 'AppPasswordParameterArn', 'MigrationPasswordParameterArn']:
        target = 'Restore' + name
        params[target] = {'Type': 'String', 'AllowedPattern': r'arn:aws:ssm:us-west-2:[0-9]{12}:parameter/course-k/blog-production/restore-[a-z-]+'}
        names[name] = target
    template['Conditions']['RestoreExercise'] = {'Fn::Equals': [ref('EnableRestoreExercise'), 'true']}
    template['Conditions']['RestoreTools'] = {'Fn::And': [{'Condition': 'RestoreExercise'}, {'Condition': 'DatabaseTools'}, {'Fn::Equals': [ref('EnableRestoreTasks'), 'true']}]}
    required = ['RestoreCheckImage', 'RestoreBackupId', *[value for key, value in names.items() if key.endswith('ParameterArn')]]
    template['Rules']['RestoreToolsRequireInputs'] = {
        'RuleCondition': {'Fn::Equals': [ref('EnableRestoreTasks'), 'true']},
        'Assertions': [{'Assert': {'Fn::Not': [{'Fn::Equals': [ref(name), '']}]}, 'AssertDescription': name + ' required for recovery tasks.'} for name in required] + [
            {'Assert': {'Fn::Equals': [ref(name), 'true']}, 'AssertDescription': name + ' required for recovery tasks.'}
            for name in ['EnableRestoreExercise', 'EnableDatabaseTools']
        ],
    }

    database = copy.deepcopy(resources['Database'])
    database.update(Condition='RestoreExercise', DeletionPolicy='Delete', UpdateReplacePolicy='Delete')
    database['Properties'].update(DBName='blog_restore', BackupRetentionPeriod=0, DeletionProtection=False)
    resources['RestoreDatabase'] = database
    bucket = copy.deepcopy(resources['ImagesBucket'])
    bucket.update(Condition='RestoreExercise', DeletionPolicy='Delete', UpdateReplacePolicy='Delete')
    # These are disposable copies. Unversioned objects can be removed by the
    # checker after verification, so CloudFormation can delete the empty bucket.
    bucket['Properties'].pop('VersioningConfiguration')
    resources['RestoreMediaBucket'] = bucket
    policy = renamed(resources['ImagesBucketPolicy'], {'ImagesBucket': 'RestoreMediaBucket'})
    policy['Condition'] = 'RestoreExercise'
    resources['RestoreMediaBucketPolicy'] = policy

    execution = renamed(resources['BootstrapExecutionRole'], names)
    execution['Condition'] = 'RestoreTools'
    statements = execution['Properties']['Policies'][0]['PolicyDocument']['Statement']
    next(s for s in statements if s['Action'] == ['ssm:GetParameters'])['Resource'].append(ref('RestoreDatabaseUrlParameterArn'))
    resources['RestoreExecutionRole'] = execution
    migration_execution = renamed(resources['MigrationExecutionRole'], names)
    migration_execution['Condition'] = 'RestoreTools'
    resources['RestoreMigrationExecutionRole'] = migration_execution
    task_role = copy.deepcopy(resources['MigrationTaskRole'])
    task_role['Condition'] = 'RestoreTools'
    task_role['Properties']['Policies'] = [{'PolicyName': 'restore-copies-only', 'PolicyDocument': {'Version': '2012-10-17', 'Statement': [
        {'Effect': 'Allow', 'Action': ['s3:GetObject'], 'Resource': sub('${BackupsBucket.Arn}/*')},
        {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject', 's3:DeleteObject'], 'Resource': sub('${RestoreMediaBucket.Arn}/images/*')},
        {'Effect': 'Allow', 'Action': ['s3:ListBucket'], 'Resource': [{'Fn::GetAtt': [name, 'Arn']} for name in ['BackupsBucket', 'RestoreMediaBucket']]},
    ]}}]
    resources['RestoreTaskRole'] = task_role

    task_names = dict(names, BootstrapExecutionRole='RestoreExecutionRole', MigrationExecutionRole='RestoreMigrationExecutionRole')
    bootstrap = renamed(resources['BootstrapTask'], task_names)
    bootstrap['Condition'] = 'RestoreTools'
    bootstrap['Properties']['Family'] = sub('${AWS::StackName}-restore-bootstrap')
    container = bootstrap['Properties']['ContainerDefinitions'][0]
    container['Image'] = ref('RestoreCheckImage')
    next(e for e in container['Environment'] if e['Name'] == 'DATABASE_NAME')['Value'] = 'blog_restore'
    resources['RestoreBootstrapTask'] = bootstrap
    migration = renamed(resources['MigrationTask'], task_names)
    migration['Condition'] = 'RestoreTools'
    migration['Properties']['Family'] = sub('${AWS::StackName}-restore-migration')
    resources['RestoreMigrationTask'] = migration

    verification = copy.deepcopy(migration)
    verification['Properties'].update(Family=sub('${AWS::StackName}-restore-verification'), ExecutionRoleArn={'Fn::GetAtt': ['RestoreExecutionRole', 'Arn']}, TaskRoleArn={'Fn::GetAtt': ['RestoreTaskRole', 'Arn']})
    container = verification['Properties']['ContainerDefinitions'][0]
    container.update(Name='restorecheck', Image=ref('RestoreCheckImage'), EntryPoint=['/usr/local/bin/restorecheck'], Command=['--cleanup-media'])
    container['Secrets'] = [{'Name': 'DATABASE_URL', 'ValueFrom': ref('RestoreDatabaseUrlParameterArn')}]
    container['Environment'] = [
        {'Name': 'SOURCE_DB_HOST', 'Value': {'Fn::GetAtt': ['Database', 'Endpoint.Address']}},
        {'Name': 'RESTORE_DB_HOST', 'Value': {'Fn::GetAtt': ['RestoreDatabase', 'Endpoint.Address']}},
        {'Name': 'MEDIA_BUCKET', 'Value': ref('RestoreMediaBucket')},
        {'Name': 'BACKUP_BUCKET', 'Value': ref('BackupsBucket')},
        {'Name': 'BACKUP_ID', 'Value': ref('RestoreBackupId')},
    ]
    resources['RestoreVerificationTask'] = verification
    template['Outputs'].update({
        'RestoreDbEndpoint': {'Condition': 'RestoreExercise', 'Value': {'Fn::GetAtt': ['RestoreDatabase', 'Endpoint.Address']}},
        'RestoreMediaBucket': {'Condition': 'RestoreExercise', 'Value': ref('RestoreMediaBucket')},
        **{name + 'Definition': {'Condition': 'RestoreTools', 'Value': ref(name)} for name in ['RestoreBootstrapTask', 'RestoreMigrationTask', 'RestoreVerificationTask']},
    })
    return template


if __name__ == '__main__':
    json.dump(recovery_template(), sys.stdout, ensure_ascii=False, indent=2)
    print()
