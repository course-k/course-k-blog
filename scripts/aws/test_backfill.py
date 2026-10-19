import unittest
from backfill import backfill_template
from recovery import recovery_template


class BackfillTests(unittest.TestCase):
    def test_no_existing_resource_or_permission_is_changed(self):
        t = backfill_template()
        for name, resource in recovery_template()['Resources'].items():
            self.assertEqual(t['Resources'][name], resource)
        self.assertEqual(t['Parameters']['EnableExternalBackfill']['Default'], 'false')
        task = t['Resources']['ExternalBackfillTask']
        self.assertEqual(task['Condition'], 'ExternalBackfill')
        props = task['Properties']
        self.assertEqual(props['TaskRoleArn'], {'Fn::GetAtt': ['MigrationTaskRole', 'Arn']})
        self.assertEqual(props['ExecutionRoleArn'], {'Fn::GetAtt': ['ApplicationExecutionRole', 'Arn']})
        container = props['ContainerDefinitions'][0]
        self.assertEqual(container['Secrets'], [{'Name': 'DATABASE_URL', 'ValueFrom': {'Ref': 'DatabaseUrlParameterArn'}}])
        self.assertEqual(container['Command'], ['--confirm-owner-metadata'])
        self.assertEqual(container['EntryPoint'], ['/usr/local/bin/externalimport'])


if __name__ == '__main__':
    unittest.main()
