import json
import unittest

from production import ROOT, production_template


class ProductionTemplateTest(unittest.TestCase):
    def setUp(self):
        self.template = production_template()
        self.resources = self.template['Resources']

    def test_database_uses_mysql_and_only_application_ingress(self):
        database = self.resources['Database']['Properties']
        self.assertEqual(database['Engine'], 'mysql')
        self.assertEqual(database['EngineVersion'], {'Ref': 'MysqlVersion'})
        self.assertFalse(database['PubliclyAccessible'])
        params = self.resources['DatabaseParameters']['Properties']
        self.assertEqual(params['Family'], 'mysql8.4')
        self.assertEqual(params['Parameters']['require_secure_transport'], '1')
        rules = [r['Properties'] for r in self.resources.values() if r['Type'] == 'AWS::EC2::SecurityGroupIngress' and r['Properties']['GroupId'] == {'Ref': 'DbSg'}]
        self.assertEqual(len(rules), 1)
        self.assertEqual((rules[0]['FromPort'], rules[0]['ToPort']), (3306, 3306))

    def test_runtime_cannot_start_without_owner_and_uses_no_password_secret(self):
        self.assertEqual(self.template['Parameters']['EnableRuntime']['Default'], 'false')
        self.assertIn('RuntimeRequiresOwner', self.template['Rules'])
        self.assertNotIn('AdminHashParameterArn', self.template['Parameters'])
        container = next(c for c in self.resources['ApplicationTask']['Properties']['ContainerDefinitions'] if c['Name'] == 'go')
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
        role = self.resources['ApplicationTaskRole']['Properties']['Policies'][0]['PolicyDocument']['Statement'][0]
        self.assertNotIn('s3:DeleteObjectVersion', role['Action'])
        self.assertNotIn('s3:*', json.dumps(role))
        lookup = self.resources['ApplicationTaskRole']['Properties']['Policies'][0]['PolicyDocument']['Statement'][1]
        self.assertEqual(lookup['Action'], ['s3:ListBucket'])
        self.assertEqual(lookup['Resource'], [{'Fn::GetAtt': ['ImagesBucket', 'Arn']}, {'Fn::GetAtt': ['BackupsBucket', 'Arn']}])
        self.assertNotIn('ProductionRuntimeBoundary', self.resources)
        self.assertIn('RuntimeBoundaryArn', self.template['Parameters'])
        for resource in self.resources.values():
            if resource['Type'] == 'AWS::IAM::Role':
                self.assertEqual(resource['Properties']['PermissionsBoundary'], {'Ref': 'RuntimeBoundaryArn'})

    def test_network_database_and_probe_are_preserved(self):
        base = json.loads((ROOT / 'infra/aws/validation-stack.template.json').read_text())
        for name in ['Vpc', 'Database', 'VpcLink', 'ProbeFunction']:
            self.assertEqual(self.resources[name], base['Resources'][name], name)
        self.assertEqual(self.resources['ApplicationService']['DependsOn'], ['InternetRoute', 'OwnerDomain'])
        self.assertEqual(self.resources['PublicMapping']['Properties']['ApiId'], {'Ref': 'HttpApi'})
        self.assertEqual(self.resources['OperationFailureMetric']['Properties']['FilterPattern'], '{ $.msg = "operation_failed" }')
        self.assertEqual(self.resources['NotificationSubscription']['Properties']['TopicArn'], {'Ref': 'AlarmTopic'})
        self.assertNotIn('Route53', json.dumps(self.resources))


    def test_one_runtime_task_shares_network_and_has_component_health_checks(self):
        services = [r for r in self.resources.values() if r['Type'] == 'AWS::ECS::Service']
        self.assertEqual(len(services), 1)
        service = services[0]['Properties']
        self.assertEqual(service['DesiredCount'], 1)
        self.assertEqual(service['TaskDefinition'], {'Ref': 'ApplicationTask'})
        self.assertEqual(service['NetworkConfiguration']['AwsvpcConfiguration']['SecurityGroups'], [{'Ref': 'ApplicationSg'}])
        task = self.resources['ApplicationTask']['Properties']
        self.assertEqual((task['Cpu'], task['Memory'], task['NetworkMode']), ('512', '2048', 'awsvpc'))
        containers = {c['Name']: c for c in task['ContainerDefinitions']}
        self.assertEqual(set(containers), {'node', 'go', 'router'})
        for container in containers.values():
            self.assertTrue(container['Essential'])
            self.assertIn('HealthCheck', container)
        node_env = {e['Name']: e['Value'] for e in containers['node']['Environment']}
        go_env = {e['Name']: e['Value'] for e in containers['go']['Environment']}
        self.assertEqual(node_env['API_BASE_URL'], 'http://127.0.0.1:8081')
        self.assertEqual(node_env['HOST'], '127.0.0.1')
        self.assertEqual(go_env['LISTEN_ADDR'], '127.0.0.1:8081')
        self.assertNotIn('Secrets', containers['node'])
        router = containers['router']
        self.assertTrue(router['ReadonlyRootFilesystem'])
        self.assertEqual(router['DependsOn'], [{'ContainerName': name, 'Condition': 'HEALTHY'} for name in ['node', 'go']])
        self.assertEqual(task['TaskRoleArn'], {'Fn::GetAtt': ['ApplicationTaskRole', 'Arn']})
        # Only necessary DB credentials and three exact ECR repositories may be read.
        statements = self.resources['ApplicationExecutionRole']['Properties']['Policies'][0]['PolicyDocument']['Statement']
        self.assertEqual(statements[-1]['Resource'], [{'Ref': 'DatabaseUrlParameterArn'}])
        image_permissions = next(s for s in statements if 'ecr:BatchGetImage' in s['Action'])
        self.assertEqual(image_permissions['Resource'], [{'Fn::GetAtt': [name, 'Arn']} for name in ['NodeRepository', 'GoRepository', 'RouterRepository']])
        self.assertLess(sum(c['Cpu'] for c in containers.values()), int(task['Cpu']))
        self.assertLess(sum(c['MemoryReservation'] for c in containers.values()), int(task['Memory']))

    def test_router_is_the_only_ingress_and_runtime_images_are_required(self):
        rules = [r['Properties'] for r in self.resources.values() if r['Type'] == 'AWS::EC2::SecurityGroupIngress']
        inbound = [r for r in rules if r['GroupId'] == {'Ref': 'ApplicationSg'}]
        self.assertEqual(inbound, [{'GroupId': {'Ref': 'ApplicationSg'}, 'SourceSecurityGroupId': {'Ref': 'LinkSg'}, 'IpProtocol': 'tcp', 'FromPort': 8080, 'ToPort': 8080}])
        db = [r for r in rules if r['GroupId'] == {'Ref': 'DbSg'}]
        self.assertEqual(db[0]['SourceSecurityGroupId'], {'Ref': 'ApplicationSg'})
        registry = self.resources['ApplicationService']['Properties']['ServiceRegistries']
        self.assertEqual(registry, [{'RegistryArn': {'Fn::GetAtt': ['ApplicationDiscovery', 'Arn']}, 'ContainerName': 'router', 'ContainerPort': 8080}])
        integrations = [r['Properties'] for r in self.resources.values() if r['Type'] == 'AWS::ApiGatewayV2::Integration']
        self.assertEqual(len(integrations), 1)
        self.assertEqual(integrations[0]['IntegrationUri'], {'Fn::GetAtt': ['ApplicationDiscovery', 'Arn']})
        assertions = self.template['Rules']['RuntimeRequiresImages']['Assertions']
        self.assertEqual([a['Assert']['Fn::Not'][0]['Fn::Equals'][0] for a in assertions], [{'Ref': n} for n in ['NodeImage', 'GoImage', 'RouterImage']])
        alarms = [r for r in self.resources.values() if r['Type'] == 'AWS::CloudWatch::Alarm']
        self.assertEqual(len(alarms), 7)
        # Removed logical IDs must not survive in references, roles or alarm dimensions.
        encoded = json.dumps(self.template)
        for obsolete in ['NodeTask', 'GoTask', 'NodeService', 'GoService', 'NodeSg', 'GoSg', 'NodeDiscovery', 'GoDiscovery', 'NodeIntegration', 'GoIntegration']:
            self.assertNotIn('"' + obsolete + '"', encoded)

if __name__ == '__main__':
    unittest.main()
