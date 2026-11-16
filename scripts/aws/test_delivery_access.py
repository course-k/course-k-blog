import json
import unittest

from delivery_access import delivery_access_template


def forwarded(statement):
    return statement.get('Condition', {}).get('ForAnyValue:StringEquals', {}).get('aws:CalledVia') == 'cloudformation.amazonaws.com'


class DeliveryAccessTest(unittest.TestCase):
    def setUp(self):
        self.template = delivery_access_template()
        self.role = self.template['Resources']['DeliveryRole']['Properties']
        self.statements = self.role['Policies'][0]['PolicyDocument']['Statement'] + self.template['Resources']['ReleasePolicy']['Properties']['PolicyDocument']['Statement']
        self.by_sid = {s['Sid']: s for s in self.statements}

    def test_only_identity_resources_and_main_branch_trust(self):
        self.assertEqual({r['Type'] for r in self.template['Resources'].values()},
                         {'AWS::IAM::OIDCProvider', 'AWS::IAM::Role', 'AWS::S3::Bucket', 'AWS::S3::BucketPolicy', 'AWS::IAM::ManagedPolicy'})
        trust = self.role['AssumeRolePolicyDocument']['Statement'][0]
        self.assertEqual(trust['Action'], 'sts:AssumeRoleWithWebIdentity')
        claims = trust['Condition']['StringEquals']
        self.assertEqual(claims['token.actions.githubusercontent.com:aud'], 'sts.amazonaws.com')
        self.assertEqual(claims['token.actions.githubusercontent.com:sub'], [
            'repo:course-k/course-k-blog:ref:refs/heads/main',
            'repo:course-k@50759350/course-k-blog@1421392480:ref:refs/heads/main',
        ])
        self.assertNotIn('*', json.dumps(claims))

    def test_no_direct_data_identity_or_infrastructure_administration(self):
        actions = {a for s in self.statements for a in s['Action']}
        self.assertFalse(any(a.split(':')[0] in {'secretsmanager', 'route53', 'cognito-idp', 'ec2', 'rds', 'apigateway'}
                             for a in actions))
        self.assertEqual({a for a in actions if a.startswith('iam:')}, {'iam:PassRole', 'iam:GetRole'})
        self.assertFalse(any(a.endswith('*') for a in actions))
        for action in ('sts:AssumeRole', 'cloudformation:DeleteStack', 'lambda:CreateFunction', 'lambda:DeleteFunction',
                       'lambda:UpdateFunctionConfiguration', 'lambda:AddPermission', 'cloudfront:UpdateDistribution',
                       'ecr:SetRepositoryPolicy', 'ecs:CreateService'):
            self.assertNotIn(action, actions)

    def test_functions_change_only_through_the_stack_and_only_the_blog_functions(self):
        update = self.by_sid['UpdateFunctionsThroughStack']
        self.assertTrue(forwarded(update))
        self.assertIn('lambda:UpdateFunctionCode', update['Action'])
        functions = json.dumps(update['Resource'])
        self.assertIn('function:course-k-blog-production-api', functions)
        self.assertIn('function:course-k-blog-production-web', functions)
        self.assertNotIn('relay', functions)
        self.assertNotIn('*', functions)
        invoke = self.by_sid['RunPendingWork']
        self.assertEqual(invoke['Action'], ['lambda:InvokeFunction'])
        self.assertTrue(json.dumps(invoke['Resource']).endswith('course-k-blog-production-api"}'))

    def test_purging_pages_is_the_only_direct_distribution_action(self):
        actions = {a for s in self.statements if not forwarded(s) for a in s['Action'] if a.startswith('cloudfront:')}
        self.assertEqual(actions, {'cloudfront:CreateInvalidation', 'cloudfront:GetInvalidation'})
        read = self.by_sid['ReadDistributionThroughStack']
        self.assertTrue(forwarded(read))
        self.assertFalse([a for a in read['Action'] if not a.startswith('cloudfront:Get')])

    def test_task_definitions_and_roles_are_the_on_demand_tools_only(self):
        register = self.by_sid['RegisterDefinitionsThroughStack']
        self.assertTrue(forwarded(register))
        text = json.dumps(register['Resource'])
        self.assertIn('on-demand-migration:*', text)
        self.assertIn('on-demand-restore:*', text)
        self.assertNotIn('bootstrap', text)
        passed = self.by_sid['PassTaskRoles']
        self.assertEqual(passed['Condition']['StringEquals']['iam:PassedToService'], 'ecs-tasks.amazonaws.com')
        self.assertEqual(len(passed['Resource']), 4)
        self.assertNotIn('Bootstrap', json.dumps(passed))
        self.assertNotIn('ApiRole', json.dumps(passed))
        roles = self.by_sid['ReadRuntimeRolesThroughStack']
        self.assertTrue(forwarded(roles))
        self.assertNotIn('aws:RequestedRegion', json.dumps(roles['Condition']))

    def test_role_patterns_match_the_generated_role_names(self):
        # Names CloudFormation generated for the applied stack (truncated logical IDs).
        names = ['course-k-blog-production-OnDemandMigrationExecution-Q57gUSBuaJ4v',
                 'course-k-blog-production-OnDemandToolTaskRole-7a0vwZHPwwAc',
                 'course-k-blog-production-OnDemandRestoreExecutionRo-4kdzJGemcIAN',
                 'course-k-blog-production-OnDemandRestoreTaskRole-RTWhi1WOCzgT']
        patterns = [r['Fn::Sub'].rsplit('role/', 1)[1] for r in self.by_sid['PassTaskRoles']['Resource']]
        for name in names:
            self.assertTrue(any(name.startswith(p.rstrip('*')) for p in patterns), name)

    def test_cloudformation_only_reads_cannot_be_called_directly(self):
        for sid in ('ReadDefinitionsThroughStack', 'RetireDefinitionsThroughStack', 'ReadSchedulesThroughStack',
                    'ReadBootstrapRepositoryOutputThroughStack', 'ReadFunctionImagesThroughStack'):
            self.assertTrue(forwarded(self.by_sid[sid]), sid)

    def test_special_release_permissions_are_bounded(self):
        image = self.by_sid['ImageSettingsThroughStack']
        self.assertNotIn('s3:GetObject', image['Action'])
        self.assertEqual({a for a in image['Action'] if a.startswith('s3:Put')}, {'s3:PutBucketCORS', 's3:PutLifecycleConfiguration'})
        self.assertTrue(forwarded(image))
        state = self.by_sid['AppliedSchemaState']
        self.assertTrue(json.dumps(state['Resource']).endswith('applied-schema-digest"}'))
        extra = self.template['Resources']['ReleasePolicy']['Properties']['PolicyDocument']
        self.assertLessEqual(len(json.dumps(extra, separators=(',', ':'))), 6144)

    def test_inline_policy_fits_role_limit_and_no_private_values(self):
        policy = json.dumps(self.role['Policies'][0]['PolicyDocument'], separators=(',', ':'))
        self.assertLess(len(policy), 10240)
        self.assertNotIn('757412973334', json.dumps(self.template))


if __name__ == '__main__':
    unittest.main()
