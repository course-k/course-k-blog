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
                   for family in ('on-demand-migration', 'on-demand-restore')]
    cluster = sub(ecs + f'cluster/{stack}-Cluster-*')
    functions = [sub('arn:${AWS::Partition}:lambda:us-west-2:${AWS::AccountId}:function:' + f'{stack}-{name}')
                 for name in ('api', 'web')]
    # Role names are generated from the logical names and truncated to 64 characters.
    task_roles = [sub(iam + f'{stack}-{name}-*') for name in (
        'OnDemandMigrationExecution', 'OnDemandToolTaskRole', 'OnDemandRestoreExecutionRo', 'OnDemandRestoreTaskRole',
    )]
    function_roles = [sub(iam + f'{stack}-{name}-*') for name in (
        'OnDemandApiRole', 'OnDemandWebRole', 'OnDemandProbeRole',
    )]
    distribution = sub('arn:${AWS::Partition}:cloudfront::${AWS::AccountId}:distribution/*')

    def allow(sid, actions, resources, condition=regional):
        statement = {'Sid': sid, 'Effect': 'Allow', 'Action': actions, 'Resource': resources}
        return {**statement, 'Condition': condition} if condition else statement

    artifacts = sub('arn:${AWS::Partition}:s3:::course-k-blog-release-${AWS::AccountId}-${AWS::Region}')
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
        allow('AppliedSchemaState', ['ssm:GetParameter', 'ssm:PutParameter'],
              sub('arn:${AWS::Partition}:ssm:us-west-2:${AWS::AccountId}:parameter/course-k/blog-production/applied-schema-digest')),
        # The special release copies the stack's migration definition with the
        # candidate image, then runs it in the blog cluster.
        allow('ReadMigrationDefinition', ['ecs:DescribeTaskDefinition'], '*'),
        allow('RegisterCandidateMigration', ['ecs:RegisterTaskDefinition'], definitions[0]),
        allow('RunMigration', ['ecs:RunTask'], definitions[0], {
            **regional, 'ArnLike': {'ecs:cluster': cluster},
        }),
        allow('WatchMigration', ['ecs:DescribeTasks', 'ecs:StopTask'], sub(ecs + f'task/{stack}-Cluster-*/*'), {
            **regional, 'ArnLike': {'ecs:cluster': cluster},
        }),
        allow('EcrAuthentication', ['ecr:GetAuthorizationToken'], '*'),
        allow('ReleaseImages', [
            'ecr:BatchCheckLayerAvailability', 'ecr:InitiateLayerUpload',
            'ecr:UploadLayerPart', 'ecr:CompleteLayerUpload', 'ecr:PutImage',
            'ecr:BatchGetImage', 'ecr:GetDownloadUrlForLayer',
            'ecr:DescribeImages', 'ecr:DescribeRepositories',
        ], [sub('arn:${AWS::Partition}:ecr:us-west-2:${AWS::AccountId}:repository/'
                + f'{stack}-{name}repository-*') for name in ('node', 'go')]),
        # Lambda checks that the caller may read the image it is given.
        allow('ReadFunctionImagesThroughStack', ['ecr:GetRepositoryPolicy'],
              [sub('arn:${AWS::Partition}:ecr:us-west-2:${AWS::AccountId}:repository/'
                   + f'{stack}-{name}repository-*') for name in ('node', 'go')], through_stack),
        # Before and after a release: which image each function runs.
        allow('ReadFunctions', ['lambda:GetFunction', 'lambda:GetFunctionConfiguration'], functions),
        # The pending work takes a due backup before images change.
        allow('RunPendingWork', ['lambda:InvokeFunction'], functions[0]),
        allow('UpdateFunctionsThroughStack', [
            'lambda:UpdateFunctionCode', 'lambda:ListTags', 'lambda:TagResource', 'lambda:UntagResource',
            'lambda:GetFunctionCodeSigningConfig', 'lambda:GetRuntimeManagementConfig',
            'lambda:GetFunctionRecursionConfig', 'lambda:GetFunctionUrlConfig', 'lambda:GetPolicy',
        ], functions, through_stack),
        # Readers' pages stay at the edge for a day; a release purges them.
        allow('PurgePages', ['cloudfront:CreateInvalidation', 'cloudfront:GetInvalidation'], distribution, None),
        # Resources that refer to the functions or the distribution are re-read
        # on an image update; their values do not change.
        allow('ReadDistributionThroughStack', ['cloudfront:GetDistribution', 'cloudfront:GetDistributionConfig'],
              distribution, {'ForAnyValue:StringEquals': {'aws:CalledVia': 'cloudformation.amazonaws.com'}}),
        allow('ReadSchedulesThroughStack', ['events:DescribeRule', 'events:ListTargetsByRule'],
              sub('arn:${AWS::Partition}:events:us-west-2:${AWS::AccountId}:rule/' + f'{stack}-OnDemand*'), through_stack),
        allow('ReadBootstrapRepositoryOutputThroughStack', ['ecr:DescribeRepositories'],
              [sub('arn:${AWS::Partition}:ecr:us-west-2:${AWS::AccountId}:repository/'
                   + f'{stack}-{name}repository-*') for name in ('bootstrap', 'router')], through_stack),
        allow('ReadDefinitionTags', ['ecs:ListTagsForResource'], definitions),
        # These APIs have no resource-level IAM support. Permit only forwarded
        # CloudFormation calls; the role can operate only the existing blog stack.
        allow('ReadDefinitionsThroughStack', ['ecs:DescribeTaskDefinition'], '*', through_stack),
        allow('RetireDefinitionsThroughStack', ['ecs:DeregisterTaskDefinition'], '*', through_stack),
        allow('RegisterDefinitionsThroughStack', [
            'ecs:RegisterTaskDefinition', 'ecs:TagResource', 'ecs:UntagResource',
        ], definitions, through_stack),
        # IAM uses a global endpoint; do not impose the regional condition.
        allow('ReadRuntimeRolesThroughStack', ['iam:GetRole'], task_roles + function_roles, {
            'ForAnyValue:StringEquals': {'aws:CalledVia': 'cloudformation.amazonaws.com'},
        }),
        allow('PassTaskRoles', ['iam:PassRole'], task_roles,
              {'StringEquals': {'iam:PassedToService': 'ecs-tasks.amazonaws.com'}}),
    ]
    extra_sids = {'AppliedSchemaState', 'ReadMigrationDefinition', 'RegisterCandidateMigration', 'RunMigration', 'WatchMigration'}
    release_statements = [s for s in statements if s['Sid'] in extra_sids]
    statements = [s for s in statements if s['Sid'] not in extra_sids]
    return {
        'AWSTemplateFormatVersion': '2010-09-09',
        'Description': 'GitHub main branch application delivery access only. No runtime resources.',
        'Resources': {
            'ReleasePolicy': {'Type': 'AWS::IAM::ManagedPolicy', 'Properties': {'PolicyDocument': {'Version': '2012-10-17', 'Statement': release_statements}}},
            'ReleaseArtifacts': {'Type': 'AWS::S3::Bucket', 'DeletionPolicy': 'Retain', 'UpdateReplacePolicy': 'Retain',
                'Properties': {'BucketName': sub('course-k-blog-release-${AWS::AccountId}-${AWS::Region}'),
                    'PublicAccessBlockConfiguration': {'BlockPublicAcls': True, 'IgnorePublicAcls': True, 'BlockPublicPolicy': True, 'RestrictPublicBuckets': True},
                    'BucketEncryption': {'ServerSideEncryptionConfiguration': [{'ServerSideEncryptionByDefault': {'SSEAlgorithm': 'AES256'}}]},
                    'LifecycleConfiguration': {'Rules': [{'Id': 'release-templates', 'Status': 'Enabled', 'Prefix': 'templates/', 'ExpirationInDays': 30}]}}},
            'ReleaseArtifactsPolicy': {'Type': 'AWS::S3::BucketPolicy', 'Properties': {'Bucket': {'Ref': 'ReleaseArtifacts'},
                'PolicyDocument': {'Version': '2012-10-17', 'Statement': [{'Effect': 'Deny', 'Principal': '*', 'Action': 's3:*',
                    'Resource': [artifacts, sub('arn:${AWS::Partition}:s3:::course-k-blog-release-${AWS::AccountId}-${AWS::Region}/*')],
                    'Condition': {'Bool': {'aws:SecureTransport': 'false'}}}]}}},
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
                    'ManagedPolicyArns': [{'Ref': 'ReleasePolicy'}],
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
