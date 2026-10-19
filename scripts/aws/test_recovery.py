import json
import unittest

from production import production_template
from recovery import recovery_template


class RecoveryContractTests(unittest.TestCase):
    def setUp(self):
        self.template = recovery_template()
        self.resources = self.template['Resources']

    def test_live_resources_are_unchanged(self):
        for name, resource in production_template()['Resources'].items():
            self.assertEqual(self.resources[name], resource, name)
        for name in ['EnableRestoreExercise', 'EnableRestoreTasks']:
            self.assertEqual(self.template['Parameters'][name]['Default'], 'false')

    def test_copy_database_is_private_encrypted_and_disposable(self):
        database = self.resources['RestoreDatabase']
        self.assertEqual(database['DeletionPolicy'], 'Delete')
        props = database['Properties']
        self.assertEqual(props['DBName'], 'blog_restore')
        self.assertFalse(props['PubliclyAccessible'])
        self.assertTrue(props['StorageEncrypted'])
        self.assertEqual(props['BackupRetentionPeriod'], 0)
        self.assertFalse(props['DeletionProtection'])

    def test_copy_bucket_can_be_emptied_without_version_deletion(self):
        bucket = self.resources['RestoreMediaBucket']
        self.assertNotIn('VersioningConfiguration', bucket['Properties'])
        self.assertTrue(all(bucket['Properties']['PublicAccessBlockConfiguration'].values()))
        policy = json.dumps(self.resources['RestoreMediaBucketPolicy'])
        self.assertIn('RestoreMediaBucket', policy)
        self.assertNotIn('"ImagesBucket"', policy)

    def test_cleanup_can_never_delete_source_backup_objects(self):
        role = self.resources['RestoreTaskRole']['Properties']
        self.assertEqual(role['PermissionsBoundary'], {'Ref': 'RuntimeBoundaryArn'})
        statements = role['Policies'][0]['PolicyDocument']['Statement']
        deletions = [s for s in statements if 's3:DeleteObject' in s['Action']]
        self.assertEqual(len(deletions), 1)
        self.assertEqual(deletions[0]['Resource'], {'Fn::Sub': '${RestoreMediaBucket.Arn}/images/*'})

    def test_bootstrap_never_targets_live_database_or_passwords(self):
        container = self.resources['RestoreBootstrapTask']['Properties']['ContainerDefinitions'][0]
        self.assertEqual(next(e['Value'] for e in container['Environment'] if e['Name'] == 'DATABASE_NAME'), 'blog_restore')
        self.assertEqual(next(e['Value'] for e in container['Environment'] if e['Name'] == 'DATABASE_HOST'), {'Fn::GetAtt': ['RestoreDatabase', 'Endpoint.Address']})
        for secret in container['Secrets']:
            self.assertIn('Restore', json.dumps(secret['ValueFrom']))

    def test_verifier_has_explicit_target_identity_and_cleanup(self):
        task = self.resources['RestoreVerificationTask']
        self.assertEqual(task['Condition'], 'RestoreTools')
        container = task['Properties']['ContainerDefinitions'][0]
        self.assertEqual(container['EntryPoint'], ['/usr/local/bin/restorecheck'])
        self.assertEqual(container['Command'], ['--cleanup-media'])
        self.assertEqual(container['Secrets'], [{'Name': 'DATABASE_URL', 'ValueFrom': {'Ref': 'RestoreDatabaseUrlParameterArn'}}])
        values = {e['Name']: e['Value'] for e in container['Environment']}
        self.assertNotEqual(values['SOURCE_DB_HOST'], values['RESTORE_DB_HOST'])
        self.assertEqual(values['MEDIA_BUCKET'], {'Ref': 'RestoreMediaBucket'})

    def test_rollback_probe_is_only_a_definition_never_a_service_update(self):
        task = self.resources['RollbackTestTask']
        self.assertEqual(task['Condition'], 'RestoreRuntimeTest')
        self.assertEqual(task['Properties']['Family'], {'Fn::Sub': '${AWS::StackName}-rollback-probe'})
        containers = task['Properties']['ContainerDefinitions']
        self.assertEqual(next(c for c in containers if c['Name'] == 'go')['Command'], ['deployment-failure-probe'])
        self.assertEqual(self.resources['ApplicationService'], production_template()['Resources']['ApplicationService'])
        self.assertNotIn('RollbackTestTask', json.dumps(self.resources['ApplicationService']))


if __name__ == '__main__':
    unittest.main()
