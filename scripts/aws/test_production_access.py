import json
import unittest

from production_access import production_access_template


class ProductionAccessTest(unittest.TestCase):
    def setUp(self):
        self.template = production_access_template()
        self.resources = self.template['Resources']
        self.statements = [s for name, r in self.resources.items()
                           if name.startswith('OperatorPolicy')
                           for s in r['Properties']['PolicyDocument']['Statement']]

    def test_no_application_resources_or_validation_targets(self):
        self.assertEqual({r['Type'] for r in self.resources.values()},
                         {'AWS::IAM::Role', 'AWS::IAM::ManagedPolicy', 'AWS::IAM::UserPolicy'})
        encoded = json.dumps(self.template)
        self.assertNotIn('course-k-blog-validation-', encoded)
        self.assertNotIn('course-k/blog-validation/', encoded)
        self.assertEqual(self.resources['OperatorRole']['Properties']['RoleName'], 'course-k-production-operator')
        stack = next(s for s in self.statements if s.get('Sid') == 'StackLifecycle')
        self.assertIn('stack/course-k-blog-production/*', json.dumps(stack['Resource']))
        self.assertNotIn('stack/course-k-blog-production-*', json.dumps(stack['Resource']))

    def test_operator_cannot_edit_its_own_access_or_boundary(self):
        forbidden = {'iam:CreatePolicy', 'iam:CreatePolicyVersion', 'iam:SetDefaultPolicyVersion',
                     'iam:DeleteRolePermissionsBoundary', 'iam:AttachRolePolicy', 'iam:PutUserPolicy'}
        for statement in self.statements:
            self.assertFalse(forbidden.intersection(statement['Action']))
            if any(a.startswith('iam:') for a in statement['Action']) and statement.get('Sid') not in {'ServiceLinkedRoles', 'BoundaryRead'}:
                self.assertNotIn('course-k-production-operator', json.dumps(statement['Resource']))
        create = next(s for s in self.statements if s.get('Sid') == 'RoleCreateWithBoundary')
        self.assertEqual(create['Condition']['StringEquals']['iam:PermissionsBoundary'], {'Ref': 'RuntimeBoundary'})

    def test_boundary_allows_production_objects_without_version_deletion(self):
        boundary = self.resources['RuntimeBoundary']['Properties']['PolicyDocument']['Statement']
        actions = {a for s in boundary for a in s['Action']}
        self.assertTrue({'s3:GetObject', 's3:PutObject', 's3:DeleteObject', 's3:ListBucket'} <= actions)
        self.assertNotIn('s3:DeleteObjectVersion', actions)
        self.assertFalse(any(a.startswith('iam:') for a in actions))

    def test_owner_administration_requires_production_pool_tag(self):
        statement = next(s for s in self.statements if s.get('Sid') == 'OwnerAdministration')
        self.assertEqual(statement['Condition']['StringEquals']['aws:ResourceTag/Project'], 'course-k-blog-production')
        self.assertNotIn('cognito-idp:*', statement['Action'])

    def test_validation_cleanup_does_not_grant_production_data_disposal(self):
        actions = {a for s in self.statements for a in s['Action']}
        self.assertNotIn('s3:DeleteObjectVersion', actions)
        self.assertNotIn('rds:DeleteDBSnapshot', actions)

    def test_managed_policies_fit_iam_size_limit(self):
        for resource in self.resources.values():
            if resource['Type'] == 'AWS::IAM::ManagedPolicy':
                body = json.dumps(resource['Properties']['PolicyDocument'], separators=(',', ':'))
                self.assertLessEqual(len(body), 6144)


if __name__ == '__main__':
    unittest.main()
