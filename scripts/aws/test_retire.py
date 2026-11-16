import json
import unittest

from backfill import backfill_template
from retire import current_template, references


class RetireTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.applied = backfill_template()
        cls.template = current_template()
        cls.resources = cls.template['Resources']

    def test_fargate_service_gateway_and_mysql_are_gone(self):
        for name in ('ApplicationService', 'ApplicationTask', 'HttpApi', 'VpcLink', 'Namespace', 'ApplicationDiscovery',
                     'PublicDomain', 'Database', 'DbSubnets', 'Public1', 'Public2', 'Private1', 'Private2', 'ProbeFunction',
                     'MigrationTask', 'BootstrapTask', 'RestoreDatabase', 'ExternalBackfillTask', 'RollbackTestTask'):
            self.assertNotIn(name, self.resources)
        self.assertFalse([name for name, r in self.resources.items() if r['Type'].startswith(('AWS::ApiGatewayV2', 'AWS::ServiceDiscovery'))])
        self.assertFalse([name for name, r in self.resources.items() if r['Type'] == 'AWS::ECS::Service'])
        self.assertEqual([name for name, r in self.resources.items() if r['Type'] == 'AWS::RDS::DBInstance'], ['OnDemandInstance'])

    def test_the_removed_database_leaves_a_final_snapshot(self):
        # Removing the resource from the stack deletes it under the applied policy.
        self.assertEqual(self.applied['Resources']['Database']['DeletionPolicy'], 'Snapshot')
        self.assertEqual(self.resources['OnDemandCluster']['DeletionPolicy'], 'Snapshot')
        self.assertTrue(self.resources['OnDemandCluster']['Properties']['DeletionProtection'])

    def test_every_remaining_resource_is_exactly_as_applied(self):
        for name, resource in self.resources.items():
            self.assertEqual(resource, self.applied['Resources'][name], name)
        self.assertIn('OnDemandDistribution', self.resources)
        # Data, identity and images stay.
        for name in ('ImagesBucket', 'BackupsBucket', 'OwnerPool', 'OwnerClient', 'GoRepository', 'NodeRepository'):
            self.assertIn(name, self.resources)

    def test_nothing_refers_to_a_removed_name(self):
        known = set(self.resources) | set(self.template['Parameters']) | {'condition:' + c for c in self.template['Conditions']}
        for section in ('Resources', 'Outputs', 'Conditions', 'Rules'):
            for name, value in self.template.get(section, {}).items():
                missing = {r for r in references(value) - known if not r.startswith('AWS::')}
                self.assertFalse(missing, f'{section}.{name}: {missing}')
                if section in ('Resources', 'Outputs'):
                    depends = value.get('DependsOn', [])
                    for target in [depends] if isinstance(depends, str) else depends:
                        self.assertIn(target, self.resources, name)
                    if 'Condition' in value:
                        self.assertIn(value['Condition'], self.template['Conditions'], name)

    def test_no_switch_can_bring_the_old_service_back(self):
        for name in ('EnableRuntime', 'EnableDatabaseTools', 'EnableRestoreExercise', 'EnableExternalBackfill', 'GoImage', 'NodeImage', 'RouterImage'):
            self.assertNotIn(name, self.template['Parameters'])
        self.assertLessEqual(len(json.dumps(self.template, separators=(',', ':')).encode()), 1024 * 1024)


if __name__ == '__main__':
    unittest.main()
