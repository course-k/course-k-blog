#!/usr/bin/env python3
"""Render administrator-owned production access; never call AWS or apply policies."""
import copy
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]


def production_access_template():
    source = json.loads((ROOT / 'infra/access/validation-access-pending.template.json').read_text())
    encoded = json.dumps(source).replace('course-k-blog-validation-', 'course-k-blog-production-')
    encoded = encoded.replace('course-k/blog-validation/', 'course-k/blog-production/')
    encoded = encoded.replace('course-k-validation-operator', 'course-k-production-operator')
    encoded = encoded.replace('course-k-validation-assume-role', 'course-k-production-assume-role')
    template = json.loads(encoded)
    template['Description'] = 'Administrator-owned production access and runtime boundary. No application infrastructure or recurring compute.'
    template['Parameters']['CertificateArn'] = {
        'Type': 'String',
        'AllowedPattern': r'arn:aws:acm:us-west-2:[0-9]{12}:certificate/[a-z0-9-]+',
        'Description': 'Existing Oregon blog certificate; read access only.',
    }
    resources = template['Resources']
    boundary = resources['RuntimeBoundary']['Properties']
    boundary['Description'] = 'Administrator-owned maximum for production ECS/probe roles; no IAM mutation.'
    boundary['PolicyDocument']['Statement'].extend([
        {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject', 's3:DeleteObject'],
         'Resource': {'Fn::Sub': 'arn:${AWS::Partition}:s3:::course-k-blog-production-*/*'}},
        {'Effect': 'Allow', 'Action': ['s3:ListBucket'],
         'Resource': {'Fn::Sub': 'arn:${AWS::Partition}:s3:::course-k-blog-production-*'}},
    ])
    for name in ('OperatorPolicy1', 'OperatorPolicy2', 'OperatorPolicy3'):
        policy = resources[name]['Properties']['PolicyDocument']
        # The fixed stack name has no suffix; generated resource names do.
        text = json.dumps(policy).replace('stack/course-k-blog-production-*/*', 'stack/course-k-blog-production/*')
        resources[name]['Properties']['PolicyDocument'] = json.loads(text)
    # Disposal of production backups is an administrator operation, unlike
    # cleanup of disposable validation data. Do not carry those grants over.
    cleanup = {'CleanupRetainedImages', 'CleanupRetainedStorage',
               'CleanupRetainedLogs', 'CleanupValidationSnapshots'}
    policy = resources['OperatorPolicy3']['Properties']['PolicyDocument']
    policy['Statement'] = [s for s in policy['Statement'] if s.get('Sid') not in cleanup]
    gateway = next(s for s in resources['OperatorPolicy1']['Properties']['PolicyDocument']['Statement']
                   if s.get('Sid') == 'GatewayThroughStack')
    gateway['Resource'].append({'Fn::Sub': 'arn:${AWS::Partition}:apigateway:us-west-2::/domainnames*'})
    via_stack = {'StringEquals': {'aws:RequestedRegion': 'us-west-2'},
                 'ForAnyValue:StringEquals': {'aws:CalledVia': 'cloudformation.amazonaws.com'}}
    regional = {'StringEquals': {'aws:RequestedRegion': 'us-west-2'}}
    pool = {'Fn::Sub': 'arn:${AWS::Partition}:cognito-idp:us-west-2:${AWS::AccountId}:userpool/*'}
    owner_pool = copy.deepcopy(regional)
    owner_pool['StringEquals']['aws:ResourceTag/Project'] = 'course-k-blog-production'
    statements = [
        {'Sid': 'AccessConfigurationRead', 'Effect': 'Allow', 'Action': ['cloudformation:DescribeStacks'],
         'Resource': {'Fn::Sub': 'arn:${AWS::Partition}:cloudformation:us-west-2:${AWS::AccountId}:stack/course-k-blog-production-access/*'},
         'Condition': regional},
        {'Sid': 'BoundaryRead', 'Effect': 'Allow', 'Action': ['iam:GetPolicy', 'iam:GetPolicyVersion'],
         'Resource': {'Ref': 'RuntimeBoundary'}},
        {'Sid': 'CertificateRead', 'Effect': 'Allow', 'Action': ['acm:DescribeCertificate'],
         'Resource': {'Ref': 'CertificateArn'}, 'Condition': regional},
        {'Sid': 'IdentityThroughStack', 'Effect': 'Allow',
         'Action': ['cognito-idp:CreateUserPool', 'cognito-idp:DescribeUserPool', 'cognito-idp:UpdateUserPool',
                    'cognito-idp:SetUserPoolMfaConfig', 'cognito-idp:GetUserPoolMfaConfig',
                    'cognito-idp:DeleteUserPool', 'cognito-idp:CreateUserPoolClient', 'cognito-idp:DescribeUserPoolClient',
                    'cognito-idp:UpdateUserPoolClient', 'cognito-idp:DeleteUserPoolClient',
                    'cognito-idp:CreateUserPoolDomain', 'cognito-idp:DescribeUserPoolDomain',
                    'cognito-idp:UpdateUserPoolDomain', 'cognito-idp:DeleteUserPoolDomain',
                    'cognito-idp:TagResource', 'cognito-idp:UntagResource', 'cognito-idp:ListTagsForResource'],
         'Resource': '*', 'Condition': via_stack},
        {'Sid': 'OwnerAdministration', 'Effect': 'Allow',
         'Action': ['cognito-idp:AdminCreateUser', 'cognito-idp:AdminGetUser', 'cognito-idp:ListUsers',
                    'cognito-idp:AdminDisableUser', 'cognito-idp:AdminEnableUser',
                    'cognito-idp:AdminUserGlobalSignOut', 'cognito-idp:AdminResetUserPassword',
                    'cognito-idp:AdminSetUserMFAPreference', 'cognito-idp:AdminUpdateUserAttributes'],
         'Resource': pool, 'Condition': owner_pool},
        {'Sid': 'MetricsThroughStack', 'Effect': 'Allow',
         'Action': ['logs:PutMetricFilter', 'logs:DeleteMetricFilter', 'logs:DescribeMetricFilters'],
         'Resource': {'Fn::Sub': 'arn:${AWS::Partition}:logs:us-west-2:${AWS::AccountId}:log-group:course-k-blog-production-*'},
         'Condition': via_stack},
        {'Sid': 'SubscriptionThroughStack', 'Effect': 'Allow',
         'Action': ['sns:Subscribe', 'sns:Unsubscribe', 'sns:GetSubscriptionAttributes', 'sns:SetSubscriptionAttributes'],
         'Resource': '*', 'Condition': via_stack},
    ]
    resources['OperatorPolicy4'] = {'Type': 'AWS::IAM::ManagedPolicy', 'Properties': {
        'PolicyDocument': {'Version': '2012-10-17', 'Statement': statements}}}
    resources['OperatorRole']['Properties']['ManagedPolicyArns'].append({'Ref': 'OperatorPolicy4'})
    return template


if __name__ == '__main__':
    json.dump(production_access_template(), sys.stdout, ensure_ascii=False, indent=2)
    print()
