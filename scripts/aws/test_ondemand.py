import copy
import json
import unittest
from unittest import mock

import production
from ondemand import ACCEPTANCE_CALLBACK, ACCEPTANCE_LOGOUT, ACCEPTANCE_ORIGIN, CACHING_DISABLED, ROOT
from production import production_template
from production_access import production_access_template

EDGE = 'cloudfront.amazonaws.com'
DISTRIBUTION_ARN = {'Fn::Sub': 'arn:${AWS::Partition}:cloudfront::${AWS::AccountId}:distribution/${OnDemandDistribution}'}


def statements(role):
    return [s for p in role['Properties']['Policies'] for s in p['PolicyDocument']['Statement']]


class OnDemandTemplateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.template = production_template()
        cls.resources = cls.template['Resources']
        # The same template without the on-demand addition.
        with mock.patch.object(production, 'add_on_demand', lambda template: template):
            cls.before = production_template()
        cls.added = {name: value for name, value in cls.resources.items() if name not in cls.before['Resources']}

    def test_nothing_new_exists_unless_switched_on(self):
        self.assertEqual(self.template['Parameters']['EnableOnDemand']['Default'], 'false')
        self.assertTrue(self.added)
        for name, resource in self.added.items():
            self.assertTrue(name.startswith('OnDemand'), name)
            self.assertIn(resource.get('Condition'), {'OnDemand', 'OnDemandRuntime', 'OnDemandMigration', 'OnDemandBootstrap', 'OnDemandRestore'}, name)
        conditions = self.template['Conditions']
        on = {'Fn::Equals': [{'Ref': 'EnableOnDemand'}, 'true']}
        self.assertEqual(conditions['OnDemand'], on)
        for name in ('OnDemandRuntime', 'OnDemandMigration', 'OnDemandBootstrap', 'OnDemandRestore'):
            self.assertEqual(conditions[name]['Fn::And'][0], on, name)
        for name, output in self.template['Outputs'].items():
            if name not in self.before['Outputs']:
                self.assertTrue(output['Condition'].startswith('OnDemand'), name)

    def test_running_resources_gain_only_the_acceptance_origin(self):
        changed = {name for name, value in self.before['Resources'].items() if self.resources[name] != value}
        self.assertEqual(changed, {'OwnerClient', 'ImagesBucket'})
        restored = copy.deepcopy(self.resources)
        client = restored['OwnerClient']['Properties']
        client['CallbackURLs'].remove(ACCEPTANCE_CALLBACK)
        client['LogoutURLs'].remove(ACCEPTANCE_LOGOUT)
        restored['ImagesBucket']['Properties']['CorsConfiguration']['CorsRules'][0]['AllowedOrigins'].remove(ACCEPTANCE_ORIGIN)
        for name in changed:
            self.assertEqual(restored[name], self.before['Resources'][name], name)
        # Each addition disappears when the on-demand side has no origin of its own.
        for entry in (ACCEPTANCE_ORIGIN, ACCEPTANCE_CALLBACK, ACCEPTANCE_LOGOUT):
            self.assertEqual(entry['Fn::If'][0], 'OnDemandOriginSet')
            self.assertEqual(entry['Fn::If'][2], {'Ref': 'AWS::NoValue'})
        self.assertNotEqual(self.before['Parameters'], self.template['Parameters'])
        for name, value in self.before['Parameters'].items():
            self.assertEqual(self.template['Parameters'][name], value, name)

    def test_database_pauses_is_private_and_is_reached_only_by_the_api_and_tasks(self):
        cluster = self.resources['OnDemandCluster']
        properties = cluster['Properties']
        self.assertEqual(properties['Engine'], 'aurora-mysql')
        self.assertEqual(properties['ServerlessV2ScalingConfiguration'], {'MinCapacity': 0, 'MaxCapacity': 1, 'SecondsUntilAutoPause': 1800})
        self.assertTrue(properties['StorageEncrypted'] and properties['DeletionProtection'] and properties['ManageMasterUserPassword'])
        self.assertEqual(cluster['DeletionPolicy'], 'Snapshot')
        self.assertNotIn('MasterUserPassword', properties)
        self.assertFalse(self.resources['OnDemandInstance']['Properties']['PubliclyAccessible'])
        self.assertNotIn('SecurityGroupIngress', self.resources['OnDemandDbSg']['Properties'])
        ingress = {name: r['Properties'] for name, r in self.added.items() if r['Type'] == 'AWS::EC2::SecurityGroupIngress'}
        self.assertEqual({p['SourceSecurityGroupId']['Ref'] for p in ingress.values()}, {'OnDemandFunctionSg', 'ApplicationSg'})
        for rule in ingress.values():
            self.assertEqual((rule['GroupId'], rule['FromPort'], rule['ToPort']), ({'Ref': 'OnDemandDbSg'}, 3306, 3306))
            self.assertNotIn('CidrIp', rule)

    def test_network_has_no_nat_and_leaves_running_subnets_alone(self):
        types = {r['Type'] for r in self.added.values()}
        self.assertNotIn('AWS::EC2::NatGateway', types)
        self.assertNotIn('AWS::EC2::EIP', types)
        for name in ('OnDemandSubnet1', 'OnDemandSubnet2'):
            subnet = self.resources[name]['Properties']
            self.assertFalse(subnet['MapPublicIpOnLaunch'])
            self.assertIn('Ipv6CidrBlock', subnet)
        existing = {r['Properties']['CidrBlock'] for r in self.before['Resources'].values() if r['Type'] == 'AWS::EC2::Subnet'}
        new = {self.resources[n]['Properties']['CidrBlock'] for n in ('OnDemandSubnet1', 'OnDemandSubnet2')}
        self.assertEqual(len(new), 2)
        self.assertFalse(existing & new)
        self.assertEqual(self.resources['OnDemandIpv6Route']['Properties']['EgressOnlyInternetGatewayId'], {'Ref': 'OnDemandEgress'})
        self.assertTrue(self.resources['OnDemandApiFunction']['Properties']['VpcConfig']['Ipv6AllowedForDualStack'])
        self.assertNotIn('VpcConfig', self.resources['OnDemandWebFunction']['Properties'])
        self.assertNotIn('VpcConfig', self.resources['OnDemandRelayFunction']['Properties'])

    def test_function_urls_accept_only_signed_requests_from_this_distribution(self):
        urls = {name: r['Properties'] for name, r in self.added.items() if r['Type'] == 'AWS::Lambda::Url'}
        self.assertEqual(set(urls), {'OnDemandApiUrl', 'OnDemandWebUrl'})
        for url in urls.values():
            self.assertEqual(url['AuthType'], 'AWS_IAM')
        permissions = [r['Properties'] for r in self.added.values() if r['Type'] == 'AWS::Lambda::Permission']
        by_action = {}
        for permission in permissions:
            by_action.setdefault(permission['Action'], []).append(permission)
        self.assertEqual(len(by_action['lambda:InvokeFunctionUrl']), 2)
        for permission in by_action['lambda:InvokeFunctionUrl']:
            self.assertEqual((permission['Principal'], permission['SourceArn']), (EDGE, DISTRIBUTION_ARN))
        scheduled = {json.dumps(p['FunctionName']): p for p in by_action['lambda:InvokeFunction']}
        self.assertEqual(len(scheduled), 2)
        work = scheduled[json.dumps({'Ref': 'OnDemandApiFunction'})]
        self.assertEqual((work['Principal'], work['SourceArn']), ('events.amazonaws.com', {'Fn::GetAtt': ['OnDemandDailyWork', 'Arn']}))
        probe = scheduled[json.dumps({'Ref': 'OnDemandProbeFunction'})]
        self.assertEqual((probe['Principal'], probe['SourceArn']), ('events.amazonaws.com', {'Fn::GetAtt': ['OnDemandProbeSchedule', 'Arn']}))
        self.assertEqual(set(by_action), {'lambda:InvokeFunctionUrl', 'lambda:InvokeFunction'})
        # The relay is invoked by the API only; it has no URL and no trigger.
        self.assertFalse([p for p in permissions if p['FunctionName'] == {'Ref': 'OnDemandRelayFunction'}])

    def test_reader_bucket_is_private_and_read_only_by_this_distribution(self):
        bucket = self.resources['OnDemandPublicBucket']
        self.assertTrue(all(bucket['Properties']['PublicAccessBlockConfiguration'].values()))
        self.assertEqual(bucket['DeletionPolicy'], 'Retain')
        allowed = [s for s in self.resources['OnDemandPublicBucketPolicy']['Properties']['PolicyDocument']['Statement'] if s['Effect'] == 'Allow']
        self.assertEqual({s['Action'] for s in allowed}, {'s3:GetObject', 's3:ListBucket'})
        for statement in allowed:
            self.assertEqual(statement['Principal'], {'Service': EDGE})
            self.assertEqual(statement['Condition'], {'StringEquals': {'AWS:SourceArn': DISTRIBUTION_ARN}})

    def test_distribution_routes_and_caches_each_path_as_designed(self):
        config = self.resources['OnDemandDistribution']['Properties']['DistributionConfig']
        behaviors = {b['PathPattern']: b for b in config['CacheBehaviors']}
        self.assertEqual(list(behaviors), ['/api/images/*', '/api/*', '/data/*'])
        default = config['DefaultCacheBehavior']
        self.assertEqual(default['TargetOriginId'], 'web')
        self.assertEqual(default['CachePolicyId'], {'Ref': 'OnDemandPageCache'})
        self.assertNotIn('POST', default['AllowedMethods'])
        # The owner API and the reader data are never kept at the edge.
        self.assertEqual(behaviors['/api/*']['CachePolicyId'], CACHING_DISABLED)
        self.assertEqual(behaviors['/api/*']['TargetOriginId'], 'api')
        self.assertIn('POST', behaviors['/api/*']['AllowedMethods'])
        self.assertEqual(behaviors['/data/*']['CachePolicyId'], CACHING_DISABLED)
        self.assertEqual(behaviors['/data/*']['TargetOriginId'], 'files')
        # Published images come from the bucket first; the API decides the rest.
        self.assertEqual(behaviors['/api/images/*']['TargetOriginId'], 'images')
        (group,) = config['OriginGroups']['Items']
        self.assertEqual([m['OriginId'] for m in group['Members']['Items']], ['files', 'api'])
        self.assertEqual(group['FailoverCriteria']['StatusCodes']['Items'], [403, 404])
        for behavior in [default, *behaviors.values()]:
            self.assertEqual(behavior['ViewerProtocolPolicy'], 'redirect-to-https')
        pages = self.resources['OnDemandPageCache']['Properties']['CachePolicyConfig']
        self.assertEqual((pages['MinTTL'], pages['DefaultTTL']), (0, 0))
        self.assertEqual(pages['ParametersInCacheKeyAndForwardedToOrigin']['CookiesConfig'], {'CookieBehavior': 'none'})
        images = self.resources['OnDemandImageCache']['Properties']['CachePolicyConfig']
        self.assertEqual(images['ParametersInCacheKeyAndForwardedToOrigin']['CookiesConfig'],
                         {'CookieBehavior': 'whitelist', 'Cookies': ['coursek_session']})
        for origin in config['Origins']:
            self.assertIn('OriginAccessControlId', origin, origin['Id'])
        # The blog host name moves to the distribution only with its certificate.
        self.assertEqual(config['Aliases']['Fn::If'][0], 'OnDemandAlias')
        self.assertEqual(config['ViewerCertificate']['Fn::If'][2], {'CloudFrontDefaultCertificate': True})

    def test_api_function_is_configured_for_on_demand_work_without_embedded_secrets(self):
        function = self.resources['OnDemandApiFunction']['Properties']
        env = function['Environment']['Variables']
        self.assertEqual(function['Code'], {'ImageUri': {'Ref': 'GoImage'}})
        self.assertEqual(env['DATABASE_URL_PARAMETER'], {'Ref': 'OnDemandDatabaseUrlParameterArn'})
        self.assertNotIn('DATABASE_URL', env)
        self.assertNotIn('ADMIN_PASSWORD_HASH', env)
        self.assertEqual(env['ON_DEMAND_FUNCTION'], function['FunctionName'])
        self.assertEqual(env['FETCH_RELAY_FUNCTION'], self.resources['OnDemandRelayFunction']['Properties']['FunctionName'])
        self.assertEqual(env['AWS_LWA_PASS_THROUGH_PATH'], '/internal/work')
        self.assertEqual(env['AWS_USE_DUALSTACK_ENDPOINT'], 'true')
        self.assertEqual(env['PUBLIC_BUCKET'], {'Ref': 'OnDemandPublicBucket'})
        self.assertEqual(env['MEDIA_BUCKET'], {'Ref': 'ImagesBucket'})
        self.assertEqual(env['BACKUP_BUCKET'], {'Ref': 'BackupsBucket'})
        self.assertEqual(env['OIDC_CLIENT_ID'], {'Ref': 'OwnerClient'})
        self.assertEqual(function['LoggingConfig'], {'LogGroup': {'Ref': 'GoLogs'}})
        rule = self.resources['OnDemandDailyWork']['Properties']
        self.assertEqual(json.loads(rule['Targets'][0]['Input']), {'work': 'pending'})
        self.assertEqual(rule['Targets'][0]['Arn'], {'Fn::GetAtt': ['OnDemandApiFunction', 'Arn']})

    def test_function_roles_are_bounded_and_grant_only_what_each_function_uses(self):
        for name in ('OnDemandApiRole', 'OnDemandWebRole', 'OnDemandRelayRole'):
            self.assertEqual(self.resources[name]['Properties']['PermissionsBoundary'], {'Ref': 'RuntimeBoundaryArn'}, name)
            self.assertNotIn('ManagedPolicyArns', self.resources[name]['Properties'], name)
        for name in ('OnDemandWebRole', 'OnDemandRelayRole'):
            actions = {a for s in statements(self.resources[name]) for a in s['Action']}
            self.assertEqual(actions, {'logs:CreateLogStream', 'logs:PutLogEvents'}, name)
        api = [s for s in statements(self.resources['OnDemandApiRole']) if 'Effect' in s]
        actions = {a for s in api for a in s['Action']}
        self.assertFalse({a for a in actions if a.endswith('*')})
        self.assertFalse({a for a in actions if a.startswith(('iam:', 'rds:', 'secretsmanager:', 'kms:'))})
        invoke = next(s for s in api if s['Action'] == ['lambda:InvokeFunction'])
        self.assertEqual(len(invoke['Resource']), 2)
        writable = next(s for s in api if 's3:PutObject' in s['Action'])['Resource']
        self.assertIn({'Fn::Sub': '${OnDemandPublicBucket.Arn}/data/*'}, writable)
        self.assertIn({'Fn::Sub': '${OnDemandPublicBucket.Arn}/api/images/*'}, writable)
        self.assertNotIn({'Fn::Sub': '${OnDemandPublicBucket.Arn}/*'}, writable)

    def test_every_runtime_permission_fits_inside_the_administrator_boundary(self):
        # A permission outside the boundary is silently ineffective, so the
        # function would fail only at run time.
        boundary = production_access_template()['Resources']['RuntimeBoundary']['Properties']['PolicyDocument']['Statement']
        allowed = {action for statement in boundary for action in statement['Action']}
        roles = [name for name, r in self.added.items() if r['Type'] == 'AWS::IAM::Role']
        self.assertEqual(len(roles), 9)
        for name in roles:
            self.assertEqual(self.resources[name]['Properties']['PermissionsBoundary'], {'Ref': 'RuntimeBoundaryArn'}, name)
            for statement in statements(self.resources[name]) if 'Policies' in self.resources[name]['Properties'] else []:
                granted = statement['Fn::If'][1] if 'Fn::If' in statement else statement
                for action in granted['Action']:
                    self.assertIn(action, allowed, f'{name}: {action}')
        scoped = {json.dumps(s['Resource']) for s in boundary if s['Action'] in (['lambda:InvokeFunction'], ['cloudfront:CreateInvalidation'])}
        self.assertFalse([r for r in scoped if r == '"*"'])

    def test_probe_watches_the_distribution_without_waking_the_database(self):
        function = self.resources['OnDemandProbeFunction']['Properties']
        env = function['Environment']['Variables']
        self.assertEqual(env['ENDPOINT'], {'Fn::Sub': 'https://${OnDemandDistribution.DomainName}'})
        self.assertEqual(env['PROBE_PATHS'], '/healthz,/')
        self.assertNotIn('readyz', json.dumps(env))
        self.assertEqual(function['Code'], self.resources['ProbeFunction']['Properties']['Code'])
        self.assertEqual(function['Role'], {'Fn::GetAtt': ['OnDemandProbeRole', 'Arn']})
        for name in ('OnDemandProbeErrorAlarm', 'OnDemandProbeInvocationAlarm'):
            alarm = self.resources[name]['Properties']
            self.assertEqual(alarm['Dimensions'], [{'Name': 'FunctionName', 'Value': {'Ref': 'OnDemandProbeFunction'}}], name)
            self.assertEqual(alarm['AlarmActions'], [{'Ref': 'AlarmTopic'}], name)
        self.assertEqual(self.resources['OnDemandProbeSchedule']['Properties']['Targets'][0]['Arn'], {'Fn::GetAtt': ['OnDemandProbeFunction', 'Arn']})
        # The running probe keeps its own target and settings.
        self.assertEqual(self.resources['ProbeFunction'], self.before['Resources']['ProbeFunction'])

    def test_relay_runs_the_tested_source(self):
        embedded = self.resources['OnDemandRelayFunction']['Properties']['Code']['ZipFile']
        self.assertEqual(embedded, (ROOT / 'infra/relay/relay.py').read_text())
        self.assertLess(len(embedded), 4096)

    def test_database_tools_are_the_running_tasks_pointed_at_the_pausing_database(self):
        encoded = json.dumps(self.resources['OnDemandBootstrapTask']) + json.dumps(self.resources['OnDemandBootstrapExecutionRole'])
        self.assertNotIn('"Database"', encoded)
        self.assertIn('OnDemandCluster', encoded)
        migration = self.resources['OnDemandMigrationTask']['Properties']
        container = migration['ContainerDefinitions'][0]
        self.assertEqual(container['Command'], ['migrate'])
        self.assertEqual(container['Secrets'], [{'Name': 'DATABASE_URL', 'ValueFrom': {'Ref': 'OnDemandMigrationUrlParameterArn'}}])
        self.assertEqual(container['Image'], self.resources['MigrationTask']['Properties']['ContainerDefinitions'][0]['Image'])
        granted = json.dumps(self.resources['OnDemandMigrationExecutionRole'])
        self.assertIn('OnDemandMigrationUrlParameterArn', granted)
        self.assertNotIn('"MigrationUrlParameterArn"', granted)
        self.assertNotEqual(migration['Family'], self.resources['MigrationTask']['Properties']['Family'])

    def test_restore_task_writes_rows_as_the_application_and_cannot_delete_or_alter(self):
        restore = self.resources['OnDemandRestoreTask']['Properties']
        container = restore['ContainerDefinitions'][0]
        # The application user has no schema rights; the migration user is not used.
        self.assertEqual(container['Secrets'], [{'Name': 'DATABASE_URL', 'ValueFrom': {'Ref': 'OnDemandDatabaseUrlParameterArn'}}])
        self.assertEqual(container['Command'], ['restore'])
        env = {e['Name']: e['Value'] for e in container['Environment']}
        self.assertEqual((env['MEDIA_BUCKET'], env['BACKUP_BUCKET']), ({'Ref': 'ImagesBucket'}, {'Ref': 'BackupsBucket'}))
        self.assertEqual(restore['TaskRoleArn'], {'Fn::GetAtt': ['OnDemandRestoreTaskRole', 'Arn']})
        actions = {a for s in statements(self.resources['OnDemandRestoreTaskRole']) for a in s['Action']}
        self.assertEqual(actions, {'s3:GetObject', 's3:PutObject', 's3:ListBucket'})
        archive = next(s for s in statements(self.resources['OnDemandRestoreTaskRole']) if s['Action'] == ['s3:GetObject'])
        self.assertNotIn('s3:PutObject', archive['Action'])
        granted = json.dumps(self.resources['OnDemandRestoreExecutionRole'])
        self.assertIn('OnDemandDatabaseUrlParameterArn', granted)
        self.assertNotIn('OnDemandMigrationUrlParameterArn', granted)
        families = {self.resources[n]['Properties']['Family']['Fn::Sub'] for n in ('MigrationTask', 'OnDemandMigrationTask', 'OnDemandRestoreTask')}
        self.assertEqual(len(families), 3)


if __name__ == '__main__':
    unittest.main()
