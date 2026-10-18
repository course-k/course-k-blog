"""Offline boundary tests. No real CLI, Docker or network is invoked."""
import importlib.util
import base64
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import core
import validation

ACCOUNT = '123456789012'
STACK = 'course-k-blog-validation-test'
ARN = f'arn:aws:cloudformation:us-west-2:{ACCOUNT}:stack/{STACK}/instance-a'


def example():
    config = json.loads((core.ROOT / 'infra/aws/validation-environment.example.json').read_text())
    return json.loads(json.dumps(config).replace('000000000000', ACCOUNT).replace(
        'REPLACE_WITH_VERIFIED_VERSION', '17.11').replace('course-k-blog-validation-example', STACK))


class FakeSession:
    def __init__(self, change=None):
        self.config = example()
        self.calls = []
        self.change = change or {'StackId': ARN, 'Status': 'CREATE_COMPLETE', 'ExecutionStatus': 'AVAILABLE',
                                  'ChangeSetId': 'change-id', 'Changes': []}
        self.current = {'StackId': ARN, 'StackName': STACK, 'StackStatus': 'UPDATE_COMPLETE',
                        'Parameters': [{'ParameterKey': 'EnableRuntime', 'ParameterValue': 'false'}],
                        'Outputs': [{'OutputKey': 'BootstrapTaskDefinition', 'OutputValue': 'task-definition-a'},
                                    {'OutputKey': 'MigrationTaskDefinition', 'OutputValue': 'task-definition-b'}]}
        self.task = {'lastStatus': 'STOPPED', 'taskDefinitionArn': 'task-definition-a', 'containers': [{'exitCode': 0}]}

    def stack(self):
        return self.current

    def resources(self):
        return {'Cluster': 'cluster-a', 'Public1': 'subnet-a', 'Public2': 'subnet-b', 'GoSg': 'sg-a'}

    def call(self, *args):
        self.calls.append(args)
        if args[:2] == ('cloudformation', 'describe-change-set'):
            return self.change
        if args[:2] == ('ecs', 'describe-tasks'):
            return {'tasks': [self.task]}
        if args[:2] == ('ecs', 'run-task'):
            return {'tasks': [{'taskArn': 'task-a'}]}
        return {}


class BoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.folder = Path(self.temp.name)
        self.config = self.folder / 'environment.json'
        self.config.write_text(json.dumps(example()))

    def tearDown(self):
        self.temp.cleanup()

    def test_cross_account_arns_and_unknown_secret_fields_rejected(self):
        for mutate in [lambda c: c.update(accessKey='must-not-be-a-config-field'),
                       lambda c: c.update(stack='production'),
                       lambda c: c['parameters'].update(DatabaseUrlParameterArn='arn:aws:ssm:us-west-2:999999999999:parameter/course-k/blog-validation/url'),
                       lambda c: c['parameters'].update(EnableRuntime='true'),
                       lambda c: c['parameters'].update(AvailabilityZone2Id='usw2-az1')]:
            with self.subTest(mutate=mutate):
                config = example()
                mutate(config)
                self.config.write_text(json.dumps(config))
                with self.assertRaises(ValueError):
                    core.load_config(self.config)

    def test_plan_never_starts_cli(self):
        with patch('sys.argv', ['validation', '--config', str(self.config), 'plan']), \
             patch('subprocess.run', side_effect=AssertionError('CLI must not be used')), \
             patch('sys.stdout', new_callable=io.StringIO) as output:
            validation.main()
        self.assertFalse(json.loads(output.getvalue())['awsContacted'])

    def test_mutations_need_apply_before_authentication(self):
        for command in ['prepare', 'execute', 'run-task', 'put-secrets', 'upload-images']:
            with self.subTest(command=command), \
                 patch('sys.argv', ['validation', '--config', str(self.config), command]), \
                 patch('validation.Session', side_effect=AssertionError('must not authenticate')), \
                 patch('sys.stderr', new_callable=io.StringIO), self.assertRaises(SystemExit):
                validation.main()

    def test_inherited_credentials_and_endpoint_overrides_cleared(self):
        with patch.dict(os.environ, {'AWS_ACCESS_KEY_ID': 'sentinel', 'AWS_PROFILE': 'work',
                                     'AWS_ENDPOINT_URL_STS': 'https://foreign.invalid'}):
            env = core.cli_environment()
        self.assertNotIn('AWS_ACCESS_KEY_ID', env)
        self.assertNotIn('AWS_PROFILE', env)
        self.assertNotIn('AWS_ENDPOINT_URL_STS', env)
        self.assertEqual(env['AWS_CONFIG_FILE'], str(core.STATE / 'config'))

    def test_aws_errors_do_not_echo_sensitive_response(self):
        error = core.AwsError('ssm', 'put-parameter', 'An error occurred (AccessDeniedException) secret-sentinel')
        self.assertEqual(error.code, 'AccessDeniedException')
        self.assertNotIn('secret-sentinel', str(error))

    def test_placeholder_environment_cannot_authenticate(self):
        with patch('subprocess.run', side_effect=AssertionError('must not run')):
            with self.assertRaises(ValueError):
                core.Session(json.loads((core.ROOT / 'infra/aws/validation-environment.example.json').read_text()))

    def test_private_files_reduce_existing_permissions(self):
        path = self.folder / 'private.json'
        path.write_text('{}')
        path.chmod(0o644)
        core.private_json(path, {'local': True})
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_unsafe_changes_never_execute(self):
        for mutation in [{'Action': 'Remove'}, {'Action': 'Modify', 'Replacement': 'True'},
                         {'Action': 'Modify', 'Replacement': 'Conditional'}]:
            session = FakeSession()
            session.change['Changes'] = [{'ResourceChange': mutation}]
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                validation.execute(session, 'validation-test')
            self.assertFalse(any(c[1] == 'execute-change-set' for c in session.calls))

    def test_wrong_stack_or_pending_change_never_executes(self):
        for mutation in [{'StackId': ARN.replace(ACCOUNT, '999999999999')}, {'Status': 'CREATE_PENDING'},
                         {'ExecutionStatus': 'EXECUTE_IN_PROGRESS'}]:
            session = FakeSession()
            session.change.update(mutation)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                validation.execute(session, 'validation-test')
            self.assertFalse(any(c[1] == 'execute-change-set' for c in session.calls))

    def test_valid_change_executes_exact_reviewed_id(self):
        session = FakeSession()
        validation.execute(session, 'validation-test')
        self.assertEqual(session.calls[-1][-1], 'change-id')

    def test_foundation_only_prepares_disabled_runtime(self):
        session = FakeSession()
        with patch('validation.STATE', self.folder):
            result = validation.prepare(session, 'foundation', 'validation-foundation')
            request = json.loads((self.folder / STACK / 'validation-foundation-request.json').read_text())
        self.assertFalse(result['executed'])
        self.assertEqual(request['ChangeSetType'], 'CREATE')
        template = json.loads(request['TemplateBody'])
        self.assertEqual(template['Parameters']['EnableRuntime']['Default'], 'false')
        self.assertFalse(any(c[1] == 'execute-change-set' for c in session.calls))

    def record_task(self, session, **updates):
        record = {'stackId': ARN, 'cluster': 'cluster-a', 'taskArn': 'task-a'}
        record.update(updates)
        core.private_json(self.folder / STACK / 'bootstrap-task.json', record)

    def test_task_failure_stale_stack_or_definition_blocks_runtime(self):
        for issue in ['stale-stack', 'stale-definition', 'failed', 'running', 'no-containers']:
            session = FakeSession()
            self.record_task(session, stackId=ARN + '-old' if issue == 'stale-stack' else ARN)
            if issue == 'stale-definition': session.task['taskDefinitionArn'] = 'old-definition'
            if issue == 'failed': session.task['containers'][0]['exitCode'] = 1
            if issue == 'running': session.task['lastStatus'] = 'RUNNING'
            if issue == 'no-containers': session.task['containers'] = []
            with self.subTest(issue=issue), patch('validation.STATE', self.folder), self.assertRaises(ValueError):
                validation.prepare(session, 'runtime', 'validation-runtime')
            self.assertFalse(any(c[1] == 'create-change-set' for c in session.calls))

    def test_db_task_cannot_repeat_or_run_with_live_app(self):
        session = FakeSession()
        self.record_task(session)
        with patch('validation.STATE', self.folder), self.assertRaises(ValueError):
            validation.run_task(session, 'bootstrap')
        session.current['Parameters'][0]['ParameterValue'] = 'true'
        with patch('validation.STATE', self.folder), self.assertRaises(ValueError):
            validation.run_task(session, 'migration')
        self.assertEqual(session.calls, [])

    def test_secret_initialization_never_overwrites_existing_parameter(self):
        session = FakeSession()
        session.current['Outputs'].append({'OutputKey': 'DbEndpoint', 'OutputValue': 'db.invalid'})
        path = self.folder / 'secrets.json'
        core.private_json(path, {'appPassword': 'a' * 24, 'migrationPassword': 'b' * 24, 'adminHash': 'pbkdf2-sha256-v1$' + base64.b64encode(b'a' * 16).decode().rstrip('=') + '$' + base64.b64encode(b'b' * 32).decode().rstrip('=')})
        with patch('validation.STATE', self.folder), self.assertRaises(ValueError):
            validation.put_secrets(session, path)
        self.assertFalse(any(c[1] == 'put-parameter' for c in session.calls))

    def test_runtime_requires_both_current_successful_tasks(self):
        session = FakeSession()
        self.record_task(session)
        core.private_json(self.folder / STACK / 'migration-task.json',
                          {'stackId': ARN, 'cluster': 'cluster-a', 'taskArn': 'task-b'})
        original = session.call
        def call(*args):
            if args[:2] == ('ecs', 'describe-tasks'):
                return {'tasks': [{'lastStatus': 'STOPPED', 'containers': [{'exitCode': 0}],
                                   'taskDefinitionArn': 'task-definition-a' if args[-1] == 'task-a' else 'task-definition-b'}]}
            return original(*args)
        session.call = call
        with patch('validation.STATE', self.folder):
            validation.prepare(session, 'runtime', 'validation-runtime')
            request = json.loads((self.folder / STACK / 'validation-runtime-request.json').read_text())
        self.assertEqual(request['Parameters'], [{'ParameterKey': 'EnableRuntime', 'ParameterValue': 'true'}])
        self.assertTrue(request['UsePreviousTemplate'])
        self.assertFalse(any(c[1] == 'execute-change-set' for c in session.calls))

    def test_secrets_outside_ignored_directory_or_world_readable_rejected(self):
        session = FakeSession()
        path = self.folder / 'secrets.json'
        path.write_text('{}')
        with self.assertRaises(ValueError):
            validation.put_secrets(session, path)
        path.chmod(0o644)
        with patch('validation.STATE', self.folder), self.assertRaises(ValueError):
            validation.put_secrets(session, path)
        self.assertEqual(session.calls, [])

    def test_preflight_does_not_substitute_postgres_version(self):
        spec = importlib.util.spec_from_file_location('preflight', core.ROOT / 'scripts/aws-validation-preflight.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        calls = []
        def call(*args):
            calls.append(args)
            if args[:2] == ('sts', 'get-caller-identity'):
                return {'Account': ACCOUNT, 'Arn': f'arn:aws:sts::{ACCOUNT}:assumed-role/course-k-validation-operator/test'}
            if args[:2] == ('ec2', 'describe-availability-zones'):
                return {'AvailabilityZones': []}
            if args[:2] == ('rds', 'describe-db-engine-versions'):
                return {'DBEngineVersions': [{'EngineVersion': '17.12', 'Status': 'available'}]}
            raise AssertionError('Do not continue to other operations.')
        with self.assertRaises(ValueError):
            module.run_checks(call, ACCOUNT, 'template', '17.11')
        self.assertEqual(len(calls), 3)


if __name__ == '__main__':
    unittest.main()
