#!/usr/bin/env python3
"""Render the production extension of the tested topology without AWS access."""
import json
from pathlib import Path
import sys

from single_task import consolidate_runtime

ROOT = Path(__file__).resolve().parents[2]


def ref(name):
    return {'Ref': name}


def sub(value):
    return {'Fn::Sub': value}


def production_template():
    # Share networking and probes instead of maintaining a second topology copy.
    template = json.loads((ROOT / 'infra/aws/validation-stack.template.json').read_text())
    template['Description'] = 'Course K production blog. Runtime stays off until identity, migration and recurring charges are approved.'
    parameters = template['Parameters']
    parameters.pop('AdminHashParameterArn')
    parameters['RuntimeBoundaryArn'] = {
        'Type': 'String',
        'AllowedPattern': r'arn:aws:iam::[0-9]{12}:policy/course-k-blog-production-access-RuntimeBoundary-[a-zA-Z0-9]+',
        'Description': 'Administrator-owned boundary from course-k-blog-production-access; not editable by the operator.',
    }
    parameters['PublicOrigin'] = {'Type': 'String', 'AllowedPattern': r'https://[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', 'Description': 'Dedicated blog hostname, HTTPS, no path or trailing slash.'}
    parameters.update({
        'CertificateArn': {'Type': 'String', 'AllowedPattern': r'arn:aws:acm:us-west-2:[0-9]{12}:certificate/[a-z0-9-]+', 'Description': 'Issued regional ACM certificate covering PublicOrigin. Validate DNS before deployment.'},
        'LoginDomainPrefix': {'Type': 'String', 'AllowedPattern': '[a-z0-9][a-z0-9-]{0,61}[a-z0-9]', 'Description': 'Available Cognito hosted-login prefix, unique in Oregon.'},
        'AdminSubject': {'Type': 'String', 'Default': '', 'AllowedPattern': '^$|^[a-f0-9-]{36}$', 'Description': 'Exact sub of the owner in this pool; required before runtime activation.'},
        'NotificationEmail': {'Type': 'String', 'AllowedPattern': r'[^ @]+@[^ @]+\.[^ @]+', 'Description': 'Owner address; subscription requires recipient confirmation.'},
    })
    template.setdefault('Rules', {})['RuntimeRequiresOwner'] = {
        'RuleCondition': {'Fn::Equals': [ref('EnableRuntime'), 'true']},
        'Assertions': [{'Assert': {'Fn::Not': [{'Fn::Equals': [ref('AdminSubject'), '']}]}, 'AssertDescription': 'Identify the owner before activating runtime.'}],
    }
    resources = template['Resources']
    resources['HttpApi']['Properties']['Name'] = sub('${AWS::StackName}')
    objects = {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject', 's3:DeleteObject'], 'Resource': [sub('${ImagesBucket.Arn}/images/*'), sub('${BackupsBucket.Arn}/images/*'), sub('${BackupsBucket.Arn}/backups/*')]}
    bucket_lookup = {'Effect': 'Allow', 'Action': ['s3:ListBucket'], 'Resource': [{'Fn::GetAtt': ['ImagesBucket', 'Arn']}, {'Fn::GetAtt': ['BackupsBucket', 'Arn']}]}
    # S3 reports a missing object as 403 without ListBucket; backup must distinguish 404 from denial.
    for resource in resources.values():
        if resource['Type'] == 'AWS::IAM::Role' and 'PermissionsBoundary' in resource['Properties']:
            resource['Properties']['PermissionsBoundary'] = ref('RuntimeBoundaryArn')
    resources['GoService']['DependsOn'] = ['InternetRoute', 'OwnerDomain']
    resources['GoTaskRole']['Properties']['Policies'] = [{'PolicyName': 'private-objects', 'PolicyDocument': {'Version': '2012-10-17', 'Statement': [objects, bucket_lookup]}}]
    statement = resources['GoExecutionRole']['Properties']['Policies'][0]['PolicyDocument']['Statement'][-1]
    if statement['Action'] != ['ssm:GetParameters']:
        raise ValueError('Shared role changed; review production secret permissions')
    statement['Resource'] = [ref('DatabaseUrlParameterArn')]
    container = resources['GoTask']['Properties']['ContainerDefinitions'][0]
    container['Secrets'] = [entry for entry in container['Secrets'] if entry['Name'] != 'ADMIN_PASSWORD_HASH']
    container['Environment'].extend([
        {'Name': 'MEDIA_BUCKET', 'Value': ref('ImagesBucket')},
        {'Name': 'BACKUP_BUCKET', 'Value': ref('BackupsBucket')},
        {'Name': 'BACKUP_RETAIN', 'Value': '4'},
        {'Name': 'BACKUP_ENABLED', 'Value': 'true'},
        {'Name': 'EXTERNAL_SYNC_ENABLED', 'Value': 'true'},
        {'Name': 'OIDC_ISSUER', 'Value': sub('https://cognito-idp.${AWS::Region}.amazonaws.com/${OwnerPool}')},
        {'Name': 'OIDC_CLIENT_ID', 'Value': ref('OwnerClient')},
        {'Name': 'OIDC_ADMIN_SUBJECT', 'Value': ref('AdminSubject')},
    ])
    resources['Stage']['Properties']['DefaultRouteSettings'] = {'ThrottlingBurstLimit': 20, 'ThrottlingRateLimit': 10}
    resources['BackupsBucket']['Properties']['LifecycleConfiguration'] = {'Rules': [{
        'Id': 'retired-archive-versions', 'Status': 'Enabled', 'Prefix': 'backups/',
        'NoncurrentVersionExpiration': {'NoncurrentDays': 7}, 'AbortIncompleteMultipartUpload': {'DaysAfterInitiation': 1},
    }]}
    resources.update({
        'OwnerPool': {'Type': 'AWS::Cognito::UserPool', 'DeletionPolicy': 'Retain', 'UpdateReplacePolicy': 'Retain', 'Properties': {
            'UserPoolTier': 'LITE', 'DeletionProtection': 'ACTIVE', 'AdminCreateUserConfig': {'AllowAdminCreateUserOnly': True},
            'UserPoolTags': {'Project': 'course-k-blog-production'},
            'UsernameAttributes': ['email'], 'AutoVerifiedAttributes': ['email'], 'UsernameConfiguration': {'CaseSensitive': False},
            'MfaConfiguration': 'ON', 'EnabledMfas': ['SOFTWARE_TOKEN_MFA'],
            'AccountRecoverySetting': {'RecoveryMechanisms': [{'Name': 'verified_email', 'Priority': 1}]},
            'Policies': {'PasswordPolicy': {'MinimumLength': 16, 'RequireUppercase': True, 'RequireLowercase': True, 'RequireNumbers': True, 'RequireSymbols': True, 'TemporaryPasswordValidityDays': 1}},
        }},
        'OwnerClient': {'Type': 'AWS::Cognito::UserPoolClient', 'Properties': {
            'UserPoolId': ref('OwnerPool'), 'GenerateSecret': False, 'AllowedOAuthFlowsUserPoolClient': True,
            'AllowedOAuthFlows': ['code'], 'AllowedOAuthScopes': ['openid'], 'SupportedIdentityProviders': ['COGNITO'],
            'CallbackURLs': [sub('${PublicOrigin}/api/auth/callback')], 'LogoutURLs': [sub('${PublicOrigin}/api/auth/signed-out')],
            'PreventUserExistenceErrors': 'ENABLED', 'EnableTokenRevocation': True, 'ExplicitAuthFlows': ['ALLOW_REFRESH_TOKEN_AUTH'],
        }},
        'OwnerDomain': {'Type': 'AWS::Cognito::UserPoolDomain', 'Properties': {'UserPoolId': ref('OwnerPool'), 'Domain': ref('LoginDomainPrefix'), 'ManagedLoginVersion': 1}},
        'PublicDomain': {'Type': 'AWS::ApiGatewayV2::DomainName', 'Properties': {
            'DomainName': {'Fn::Select': [2, {'Fn::Split': ['/', ref('PublicOrigin')]}]},
            'DomainNameConfigurations': [{'CertificateArn': ref('CertificateArn'), 'EndpointType': 'REGIONAL', 'SecurityPolicy': 'TLS_1_2'}],
        }},
        'PublicMapping': {'Type': 'AWS::ApiGatewayV2::ApiMapping', 'DependsOn': 'Stage', 'Properties': {'ApiId': ref('HttpApi'), 'DomainName': ref('PublicDomain'), 'Stage': '$default'}},
        'NotificationSubscription': {'Type': 'AWS::SNS::Subscription', 'Properties': {'TopicArn': ref('AlarmTopic'), 'Protocol': 'email', 'Endpoint': ref('NotificationEmail')}},
        'OperationFailureMetric': {'Type': 'AWS::Logs::MetricFilter', 'Properties': {
            'LogGroupName': ref('GoLogs'), 'FilterPattern': '{ $.msg = "operation_failed" }',
            'MetricTransformations': [{'MetricName': 'OperationFailures', 'MetricNamespace': sub('${AWS::StackName}/Operations'), 'MetricValue': '1', 'DefaultValue': 0}],
        }},
        'OperationFailureAlarm': {'Type': 'AWS::CloudWatch::Alarm', 'Properties': {
            'Namespace': sub('${AWS::StackName}/Operations'), 'MetricName': 'OperationFailures', 'Statistic': 'Sum',
            'Period': 300, 'EvaluationPeriods': 1, 'Threshold': 1, 'ComparisonOperator': 'GreaterThanOrEqualToThreshold',
            'TreatMissingData': 'notBreaching', 'AlarmActions': [ref('AlarmTopic')], 'OKActions': [ref('AlarmTopic')],
        }},
    })
    template['Outputs'].update({
        'OwnerPoolId': {'Value': ref('OwnerPool')}, 'OwnerClientId': {'Value': ref('OwnerClient')},
        'PublicDnsTarget': {'Value': {'Fn::GetAtt': ['PublicDomain', 'RegionalDomainName']}},
        'PublicDnsZoneId': {'Value': {'Fn::GetAtt': ['PublicDomain', 'RegionalHostedZoneId']}},
        'RuntimeBoundaryArn': {'Value': ref('RuntimeBoundaryArn')},
    })
    consolidate_runtime(template)
    return template


if __name__ == '__main__':
    json.dump(production_template(), sys.stdout, ensure_ascii=False, indent=2)
    print()
