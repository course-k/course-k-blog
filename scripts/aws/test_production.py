import json
import unittest

from production import ROOT, production_template


class ProductionTemplateTest(unittest.TestCase):
    def setUp(self):
        self.template = production_template()
        self.resources = self.template['Resources']

    def test_runtime_cannot_start_without_owner_and_uses_no_password_secret(self):
        self.assertEqual(self.template['Parameters']['EnableRuntime']['Default'], 'false')
        self.assertIn('RuntimeRequiresOwner', self.template['Rules'])
        self.assertNotIn('AdminHashParameterArn', self.template['Parameters'])
        container = self.resources['GoTask']['Properties']['ContainerDefinitions'][0]
        self.assertEqual([s['Name'] for s in container['Secrets']], ['DATABASE_URL'])
        environment = {entry['Name']: entry['Value'] for entry in container['Environment']}
        self.assertEqual(environment['MEDIA_BUCKET'], {'Ref': 'ImagesBucket'})
        self.assertEqual(environment['BACKUP_BUCKET'], {'Ref': 'BackupsBucket'})
        self.assertEqual(environment['OIDC_ADMIN_SUBJECT'], {'Ref': 'AdminSubject'})
        self.assertEqual(environment['BACKUP_ENABLED'], 'true')
        self.assertEqual(environment['EXTERNAL_SYNC_ENABLED'], 'true')

    def test_authentication_requires_totp_and_disallows_public_signup(self):
        pool = self.resources['OwnerPool']['Properties']
        self.assertTrue(pool['AdminCreateUserConfig']['AllowAdminCreateUserOnly'])
        self.assertEqual(pool['MfaConfiguration'], 'ON')
        self.assertEqual(pool['EnabledMfas'], ['SOFTWARE_TOKEN_MFA'])
        client = self.resources['OwnerClient']['Properties']
        self.assertFalse(client['GenerateSecret'])
        self.assertEqual(client['AllowedOAuthFlows'], ['code'])
        self.assertEqual(client['CallbackURLs'], [{'Fn::Sub': '${PublicOrigin}/api/auth/callback'}])
        self.assertEqual(client['LogoutURLs'], [{'Fn::Sub': '${PublicOrigin}/api/auth/signed-out'}])

    def test_backup_retention_does_not_expire_referenced_image_copies(self):
        bucket = self.resources['BackupsBucket']['Properties']
        self.assertTrue(all(bucket['PublicAccessBlockConfiguration'].values()))
        rules = bucket['LifecycleConfiguration']['Rules']
        self.assertEqual(rules[0]['Prefix'], 'backups/')
        self.assertNotIn('ExpirationInDays', rules[0])
        self.assertEqual(rules[0]['NoncurrentVersionExpiration']['NoncurrentDays'], 7)
        role = self.resources['GoTaskRole']['Properties']['Policies'][0]['PolicyDocument']['Statement'][0]
        self.assertNotIn('s3:DeleteObjectVersion', role['Action'])
        boundary = self.resources['ProductionRuntimeBoundary']['Properties']['PolicyDocument']['Statement']
        self.assertIn(role, boundary)
        self.assertNotIn('s3:*', json.dumps(role))
        lookup = self.resources['GoTaskRole']['Properties']['Policies'][0]['PolicyDocument']['Statement'][1]
        self.assertEqual(lookup['Action'], ['s3:ListBucket'])
        self.assertEqual(lookup['Resource'], [{'Fn::GetAtt': ['ImagesBucket', 'Arn']}, {'Fn::GetAtt': ['BackupsBucket', 'Arn']}])
        self.assertIn(lookup, boundary)

    def test_shared_topology_and_probe_are_preserved(self):
        base = json.loads((ROOT / 'infra/aws/validation-stack.template.json').read_text())
        for name in ['Vpc', 'Database', 'NodeService', 'VpcLink', 'NodeIntegration', 'GoIntegration', 'ProbeFunction']:
            self.assertEqual(self.resources[name], base['Resources'][name], name)
        self.assertEqual(self.resources['GoService']['DependsOn'], ['InternetRoute', 'OwnerDomain'])
        self.assertEqual(self.resources['PublicMapping']['Properties']['ApiId'], {'Ref': 'HttpApi'})
        self.assertEqual(self.resources['OperationFailureMetric']['Properties']['FilterPattern'], '{ $.msg = "operation_failed" }')
        self.assertEqual(self.resources['NotificationSubscription']['Properties']['TopicArn'], {'Ref': 'AlarmTopic'})
        self.assertNotIn('Route53', json.dumps(self.resources))


if __name__ == '__main__':
    unittest.main()
