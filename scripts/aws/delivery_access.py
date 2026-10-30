#!/usr/bin/env python3
"""Render GitHub's application delivery permissions; never call AWS or deploy."""
import json
import sys


def sub(value):
    return {'Fn::Sub': value}


def delivery_access_template():
    stack = 'course-k-blog-production'
    regional = {'StringEquals': {'aws:RequestedRegion': 'us-west-2'}}
    through_stack = {
        **regional,
        'ForAnyValue:StringEquals': {'aws:CalledVia': 'cloudformation.amazonaws.com'},
    }
    ecs = 'arn:${AWS::Partition}:ecs:us-west-2:${AWS::AccountId}:'
    cfn = 'arn:${AWS::Partition}:cloudformation:us-west-2:${AWS::AccountId}:'
    iam = 'arn:${AWS::Partition}:iam::${AWS::AccountId}:role/'
    definitions = [sub(ecs + f'task-definition/{stack}-{family}:*')
                   for family in ('application', 'migration')]
    cluster = sub(ecs + f'cluster/{stack}-Cluster-*')
    service = sub(ecs + f'service/{stack}-Cluster-*/{stack}-ApplicationService-*')

    def allow(sid, actions, resources, condition=regional):
        return {'Sid': sid, 'Effect': 'Allow', 'Action': actions,
                'Resource': resources, 'Condition': condition}

    statements = [
        allow('ReadProductionStack', [
            'cloudformation:DescribeStacks', 'cloudformation:DescribeStackEvents',
            'cloudformation:ListStackResources', 'cloudformation:GetTemplate',
        ], sub(cfn + f'stack/{stack}/*')),
        allow('ApplicationChangeSets', [
            'cloudformation:CreateChangeSet', 'cloudformation:DescribeChangeSet',
            'cloudformation:ExecuteChangeSet', 'cloudformation:DeleteChangeSet',
        ], [sub(cfn + f'stack/{stack}/*'),
            sub(cfn + f'changeSet/{stack}-release-*/*')]),
        allow('EcrAuthentication', ['ecr:GetAuthorizationToken'], '*'),
        allow('ReleaseImages', [
            'ecr:BatchCheckLayerAvailability', 'ecr:InitiateLayerUpload',
            'ecr:UploadLayerPart', 'ecr:CompleteLayerUpload', 'ecr:PutImage',
            'ecr:BatchGetImage', 'ecr:GetDownloadUrlForLayer',
            'ecr:DescribeImages', 'ecr:DescribeRepositories',
        ], [sub('arn:${AWS::Partition}:ecr:us-west-2:${AWS::AccountId}:repository/'
                + f'{stack}-{name}repository-*') for name in ('node', 'go', 'router')]),
        allow('ReadApplicationService', ['ecs:DescribeServices'], service),
        allow('ReadApplicationCluster', ['ecs:DescribeClusters'], cluster),
        allow('ReadDefinitions', ['ecs:DescribeTaskDefinition', 'ecs:ListTagsForResource'], definitions),
        allow('RegisterDefinitionsThroughStack', [
            'ecs:RegisterTaskDefinition', 'ecs:DeregisterTaskDefinition', 'ecs:TagResource',
        ], definitions, through_stack),
        allow('UpdateApplicationThroughStack', ['ecs:UpdateService'], service, through_stack),
        allow('ReadApplicationTasks', ['ecs:ListTasks'], '*', {
            **regional, 'ArnLike': {'ecs:cluster': cluster},
        }),
        allow('ReadAndStopBackupTasks', ['ecs:DescribeTasks', 'ecs:StopTask'],
              sub(ecs + f'task/{stack}-Cluster-*/*'), {
                  **regional, 'ArnLike': {'ecs:cluster': cluster},
              }),
        allow('RunBackup', ['ecs:RunTask'], definitions[0], {
            **regional, 'ArnLike': {'ecs:cluster': cluster},
        }),
        allow('PassRuntimeRoles', ['iam:PassRole'], [
            sub(iam + f'{stack}-{name}-*') for name in (
                'ApplicationExecutionRole', 'ApplicationTaskRole',
                'MigrationExecutionRole', 'MigrationTaskRole',
            )
        ], {'StringEquals': {'iam:PassedToService': 'ecs-tasks.amazonaws.com'}}),
        allow('UpdateApplicationAlarmsThroughStack', [
            'cloudwatch:PutMetricAlarm', 'cloudwatch:DescribeAlarms', 'cloudwatch:ListTagsForResource',
        ], [sub('arn:${AWS::Partition}:cloudwatch:us-west-2:${AWS::AccountId}:alarm:'
                + f'{stack}-Application{name}Alarm-*')
            for name in ('CPUUtilization', 'MemoryUtilization')], through_stack),
    ]
    return {
        'AWSTemplateFormatVersion': '2010-09-09',
        'Description': 'GitHub main branch application delivery access only. No runtime resources.',
        'Resources': {
            'GitHubProvider': {
                'Type': 'AWS::IAM::OIDCProvider',
                'Properties': {'Url': 'https://token.actions.githubusercontent.com',
                               'ClientIdList': ['sts.amazonaws.com']},
            },
            'DeliveryRole': {
                'Type': 'AWS::IAM::Role',
                'Properties': {
                    'RoleName': 'course-k-blog-github-deploy',
                    'Description': 'GitHub course-k/course-k-blog main application releases',
                    'MaxSessionDuration': 3600,
                    'AssumeRolePolicyDocument': {
                        'Version': '2012-10-17',
                        'Statement': [{
                            'Effect': 'Allow',
                            'Principal': {'Federated': {'Ref': 'GitHubProvider'}},
                            'Action': 'sts:AssumeRoleWithWebIdentity',
                            'Condition': {'StringEquals': {
                                'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
                                # Support both GitHub subject formats with exact identities.
                                'token.actions.githubusercontent.com:sub': [
                                    'repo:course-k/course-k-blog:ref:refs/heads/main',
                                    'repo:course-k@50759350/course-k-blog@1421392480:ref:refs/heads/main',
                                ],
                            }},
                        }],
                    },
                    'Policies': [{'PolicyName': 'blog-application-delivery',
                                  'PolicyDocument': {'Version': '2012-10-17', 'Statement': statements}}],
                    'Tags': [{'Key': 'Project', 'Value': stack}],
                },
            },
        },
        'Outputs': {'DeliveryRoleArn': {'Value': {'Fn::GetAtt': ['DeliveryRole', 'Arn']}}},
    }


if __name__ == '__main__':
    json.dump(delivery_access_template(), sys.stdout, indent=2)
    print()
