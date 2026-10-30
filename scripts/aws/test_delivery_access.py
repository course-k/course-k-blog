import json
import unittest

from delivery_access import delivery_access_template


class DeliveryAccessTest(unittest.TestCase):
    def setUp(self):
        self.template = delivery_access_template()
        self.role = self.template['Resources']['DeliveryRole']['Properties']
        self.statements = self.role['Policies'][0]['PolicyDocument']['Statement']

    def test_only_identity_resources_and_main_branch_trust(self):
        self.assertEqual({r['Type'] for r in self.template['Resources'].values()},
                         {'AWS::IAM::OIDCProvider', 'AWS::IAM::Role'})
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
        self.assertFalse(any(a.split(':')[0] in {'s3', 'ssm', 'secretsmanager', 'rds',
                                               'route53', 'cognito-idp', 'ec2'} for a in actions))
        self.assertEqual({a for a in actions if a.startswith('iam:')}, {'iam:PassRole', 'iam:GetRole'})
        self.assertNotIn('sts:AssumeRole', actions)
        self.assertFalse(any(a.endswith('*') for a in actions))
        self.assertNotIn('cloudformation:DeleteStack', actions)
        self.assertNotIn('ecs:CreateService', actions)
        self.assertNotIn('ecs:DeleteService', actions)

    def test_changes_are_scoped_to_blog_and_cloudformation(self):
        service = next(s for s in self.statements if s['Sid'] == 'UpdateApplicationThroughStack')
        self.assertIn('course-k-blog-production-Cluster-', json.dumps(service['Resource']))
        self.assertEqual(service['Condition']['ForAnyValue:StringEquals']['aws:CalledVia'],
                         'cloudformation.amazonaws.com')
        runtime = next(s for s in self.statements if s['Sid'] == 'PassRuntimeRoles')
        self.assertEqual(runtime['Condition']['StringEquals']['iam:PassedToService'],
                         'ecs-tasks.amazonaws.com')
        self.assertEqual(len(runtime['Resource']), 4)
        self.assertNotIn('Operator', json.dumps(runtime))
        self.assertNotIn('Bootstrap', json.dumps(runtime))
        tasks = next(s for s in self.statements if s['Sid'] == 'RegisterDefinitionsThroughStack')
        self.assertNotEqual(tasks['Resource'], '*')
        self.assertEqual(len(tasks['Resource']), 2)

    def test_backup_can_only_run_application_definition_in_blog_cluster(self):
        run = next(s for s in self.statements if s['Sid'] == 'RunBackup')
        self.assertIn('-application:', json.dumps(run['Resource']))
        self.assertNotIn('migration', json.dumps(run))
        self.assertIn('ecs:cluster', run['Condition']['ArnLike'])

    def test_inline_policy_fits_role_limit_and_no_private_values(self):
        policy = json.dumps(self.role['Policies'][0]['PolicyDocument'], separators=(',', ':'))
        self.assertLess(len(policy), 10240)
        self.assertNotIn('757412973334', json.dumps(self.template))

    def test_cloudformation_required_reads_cannot_be_called_directly(self):
        read = next(s for s in self.statements if s['Sid'] == 'ReadDefinitionsThroughStack')
        self.assertEqual(read['Action'], ['ecs:DescribeTaskDefinition'])
        self.assertEqual(read['Resource'], '*')
        self.assertEqual(read['Condition']['ForAnyValue:StringEquals']['aws:CalledVia'],
                         'cloudformation.amazonaws.com')
        roles = next(s for s in self.statements if s['Sid'] == 'ReadRuntimeRolesThroughStack')
        self.assertEqual(roles['Action'], ['iam:GetRole'])
        self.assertEqual(len(roles['Resource']), 4)
        self.assertEqual(roles['Condition']['ForAnyValue:StringEquals']['aws:CalledVia'],
                         'cloudformation.amazonaws.com')


if __name__ == '__main__':
    unittest.main()
