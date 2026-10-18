#!/usr/bin/env python3
"""Explicit validation operations. plan is offline; writes require --apply."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import quote

from core import ConfigurationError, safe_diagnostic, AwsError, REGION, ROOT, STATE, TEMPLATE, Session, load_config, private_json


def state_dir(config):
    return STATE / config['stack']


def outputs(stack):
    return {x['OutputKey']: x['OutputValue'] for x in stack.get('Outputs', [])}


def completed_task(session, step):
    record = json.loads((state_dir(session.config) / (step + '-task.json')).read_text())
    stack = session.stack()
    resources = session.resources()
    if record['stackId'] != stack['StackId'] or record['cluster'] != resources['Cluster']:
        raise ConfigurationError('Task record belongs to a different stack incarnation.')
    result = session.call('ecs', 'describe-tasks', '--cluster', record['cluster'], '--tasks', record['taskArn'])
    if result.get('failures') or len(result.get('tasks', [])) != 1:
        raise ConfigurationError('Task status is unavailable.')
    task = result['tasks'][0]
    expected = outputs(stack)[step.title() + 'TaskDefinition']
    if task.get('taskDefinitionArn') != expected:
        raise ConfigurationError('Task definition changed; run and verify the current definition.')
    containers = task.get('containers', [])
    if task['lastStatus'] != 'STOPPED' or not containers or any(c.get('exitCode') != 0 for c in containers):
        raise ConfigurationError('Current ' + step + ' task has not completed successfully.')
    return task


def prepare(session, stage, name):
    config = session.config
    request = {'StackName': config['stack'], 'ChangeSetName': name, 'Capabilities': ['CAPABILITY_IAM']}
    if stage == 'foundation':
        template = json.loads(TEMPLATE.read_text())
        body = json.dumps(template, separators=(',', ':'))
        if len(body.encode()) > 51200:
            raise ConfigurationError('Template exceeds direct CloudFormation limit; do not silently upload elsewhere.')
        request.update(ChangeSetType='CREATE', TemplateBody=body,
                       Parameters=[{'ParameterKey': k, 'ParameterValue': v} for k, v in config['parameters'].items()])
    else:
        stack = session.stack()
        if stack['StackStatus'] not in ('CREATE_COMPLETE', 'UPDATE_COMPLETE', 'UPDATE_ROLLBACK_COMPLETE'):
            raise ConfigurationError('Stack must be stable before preparing a change.')
        params = {p['ParameterKey']: {'ParameterKey': p['ParameterKey'], 'UsePreviousValue': True}
                  for p in stack['Parameters']}
        updates = {}
        if stage == 'tools':
            current = {p['ParameterKey']: p['ParameterValue'] for p in stack['Parameters']}
            if current.get('EnableRuntime') != 'false':
                raise ConfigurationError('DB tools preparation is restricted to a not-yet-running validation stack.')
            images = json.loads((state_dir(config) / 'images.json').read_text())
            if images['stackId'] != stack['StackId']:
                raise ConfigurationError('Images belong to a different stack incarnation.')
            for logical, key in [('NodeRepository', 'NodeImage'), ('GoRepository', 'GoImage'),
                                 ('BootstrapRepository', 'BootstrapImage')]:
                digest = images['images'][logical]
                repository = outputs(stack)[logical]
                if not re.fullmatch(re.escape(repository) + r'@sha256:[a-f0-9]{64}', digest):
                    raise ConfigurationError('Invalid pinned image: ' + logical)
                updates[key] = digest
            # Verify all required parameters exist without requesting decrypted values.
            for p in config['parameters']:
                if p.endswith('ParameterArn'):
                    session.call('ssm', 'get-parameter', '--name', config['parameters'][p])
            updates['EnableDatabaseTools'] = 'true'
        elif stage == 'runtime':
            completed_task(session, 'bootstrap')
            completed_task(session, 'migration')
            updates['EnableRuntime'] = 'true'
        for key, value in updates.items():
            params[key] = {'ParameterKey': key, 'ParameterValue': value}
        request.update(ChangeSetType='UPDATE', UsePreviousTemplate=True, Parameters=list(params.values()))
    path = state_dir(config) / (name + '-request.json')
    private_json(path, request)
    session.call('cloudformation', 'create-change-set', '--cli-input-json', 'file://' + str(path))
    return {'prepared': name, 'executed': False, 'next': 'inspect; then explicitly execute the named change set'}


def inspect_change(session, name):
    change = session.call('cloudformation', 'describe-change-set', '--stack-name', session.config['stack'],
                          '--change-set-name', name)
    stack_arn = f'arn:aws:cloudformation:{REGION}:{session.config["account"]}:stack/{session.config["stack"]}/'
    if not change.get('StackId', '').startswith(stack_arn):
        raise ConfigurationError('Change set belongs to another account, region or stack.')
    return change


def execute(session, name):
    change = inspect_change(session, name)
    if change['Status'] != 'CREATE_COMPLETE' or change.get('ExecutionStatus') != 'AVAILABLE':
        raise ConfigurationError('Change set is not available for execution.')
    # Teardown and replacements require separate manual review, not this helper.
    for item in change.get('Changes', []):
        resource = item['ResourceChange']
        if resource['Action'] == 'Remove' or resource.get('Replacement') in ('True', 'Conditional'):
            raise ConfigurationError('Removal or potential replacement requires manual review; not executed.')
    session.call('cloudformation', 'execute-change-set', '--stack-name', session.config['stack'],
                 '--change-set-name', change['ChangeSetId'])
    return {'executionRequested': name}


def run_task(session, step):
    stack = session.stack()
    if stack['StackStatus'] not in ('CREATE_COMPLETE', 'UPDATE_COMPLETE'):
        raise ConfigurationError('Stack must be stable before running a database task.')
    if any(p['ParameterKey'] == 'EnableRuntime' and p['ParameterValue'] != 'false' for p in stack['Parameters']):
        raise ConfigurationError('Initial DB tasks cannot be run while the application is enabled.')
    if step == 'migration':
        completed_task(session, 'bootstrap')
    resources = session.resources()
    record_path = state_dir(session.config) / (step + '-task.json')
    if record_path.exists():
        old = json.loads(record_path.read_text())
        if old.get('stackId') == stack['StackId']:
            raise ConfigurationError('Task already requested for this stack; inspect before any manual rerun.')
    network = {'awsvpcConfiguration': {'subnets': [resources['Public1'], resources['Public2']],
                'securityGroups': [resources['GoSg']], 'assignPublicIp': 'ENABLED'}}
    result = session.call('ecs', 'run-task', '--cluster', resources['Cluster'],
                          '--task-definition', outputs(stack)[step.title() + 'TaskDefinition'],
                          '--launch-type', 'FARGATE', '--platform-version', '1.4.0',
                          '--network-configuration', json.dumps(network), '--count', '1',
                          '--client-token', hashlib.sha256((stack['StackId'] + step).encode()).hexdigest())
    if result.get('failures') or len(result.get('tasks', [])) != 1:
        raise ConfigurationError('Task start failed; inspect ECS before retrying.')
    private_json(record_path, {'stackId': stack['StackId'], 'cluster': resources['Cluster'],
                              'taskArn': result['tasks'][0]['taskArn']})
    return {'taskRequested': step}


def upload_images(session):
    stack = session.stack()
    out = outputs(stack)
    registry = f'{session.config["account"]}.dkr.ecr.{REGION}.amazonaws.com'
    repos = {k: v for k, v in session.resources().items() if k in session.config['images']}
    if len(repos) != 3 or any(not v.startswith(session.config['stack'].lower()) for v in repos.values()):
        raise ConfigurationError('Unexpected validation repositories.')
    endpoint = subprocess.run(['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
                              capture_output=True, text=True, check=True).stdout.strip()
    # Check ARM64 before changing any remote repository.
    for local in session.config['images'].values():
        result = subprocess.run(['docker', 'image', 'inspect', '--format', '{{.Os}}/{{.Architecture}}', local],
                                 capture_output=True, text=True, check=True)
        if result.stdout.strip() != 'linux/arm64':
            raise ConfigurationError('All images must be Linux ARM64.')
    auth = session.call('ecr', 'get-authorization-token')['authorizationData'][0]
    images = {}
    with tempfile.TemporaryDirectory(prefix='course-k-ecr-') as folder:
        private_json(Path(folder) / 'config.json', {'auths': {registry: {'auth': auth['authorizationToken']}}})
        env = os.environ.copy()
        env.pop('DOCKER_CONTEXT', None)
        env.update(DOCKER_CONFIG=folder, DOCKER_HOST=endpoint)
        for logical, local in session.config['images'].items():
            # Content-derived tag supports immutable repositories without dated hardcoding.
            image_id = subprocess.run(['docker', 'image', 'inspect', '--format', '{{.Id}}', local],
                                       capture_output=True, text=True, check=True).stdout.strip()
            tag = 'validation-' + image_id.removeprefix('sha256:')
            target = registry + '/' + repos[logical] + ':' + tag
            for command in (['docker', 'tag', local, target], ['docker', 'push', target]):
                result = subprocess.run(command, env=env, capture_output=True, text=True)
                if result.returncode:
                    raise RuntimeError('Docker image upload failed; raw registry output suppressed.')
            record = session.call('ecr', 'describe-images', '--repository-name', repos[logical],
                                  '--image-ids', 'imageTag=' + tag)['imageDetails'][0]
            images[logical] = registry + '/' + repos[logical] + '@' + record['imageDigest']
    private_json(state_dir(session.config) / 'images.json', {'stackId': stack['StackId'], 'images': images})
    return {'pinnedImages': sorted(images)}


def put_secrets(session, path):
    # File is intentionally separate from non-secret environment configuration.
    path = Path(path).resolve()
    if not path.is_relative_to(STATE.resolve()) or path.stat().st_mode & 0o077:
        raise ConfigurationError('Secrets file must be private (0600) and inside ignored config/aws-validation/.')
    values = json.loads(path.read_text())
    expected = {'appPassword', 'migrationPassword', 'adminHash'}
    if set(values) != expected or not all(isinstance(v, str) and v for v in values.values()):
        raise ConfigurationError('Secrets file requires only appPassword, migrationPassword and adminHash.')
    parts = values['adminHash'].split('$')
    try:
        valid_hash = (len(parts) == 3 and parts[0] == 'pbkdf2-sha256-v1'
                      and len(base64.b64decode(parts[1] + '==', validate=True)) == 16
                      and len(base64.b64decode(parts[2] + '=', validate=True)) == 32)
    except ValueError:
        valid_hash = False
    if not valid_hash:
        raise ConfigurationError('Use the administrator hash produced by backend/cmd/password.')
    if min(len(values['appPassword']), len(values['migrationPassword'])) < 24 or values['appPassword'] == values['migrationPassword']:
        raise ConfigurationError('Use two different DB passwords of at least 24 characters.')
    stack = session.stack()
    if stack['StackStatus'] not in ('CREATE_COMPLETE', 'UPDATE_COMPLETE') or any(
            p['ParameterKey'] == 'EnableRuntime' and p['ParameterValue'] != 'false' for p in stack['Parameters']):
        raise ConfigurationError('Secret initialization requires a stable, not-yet-running validation stack.')
    endpoint = outputs(stack)['DbEndpoint']
    def dsn(user, password):
        return f'postgresql://{user}:{quote(password, safe="")}@{endpoint}:5432/blog?sslmode=verify-full'
    payloads = {'DatabaseUrlParameterArn': dsn('blog_app', values['appPassword']),
                'MigrationUrlParameterArn': dsn('blog_migrator', values['migrationPassword']),
                'AdminHashParameterArn': values['adminHash'], 'AppPasswordParameterArn': values['appPassword'],
                'MigrationPasswordParameterArn': values['migrationPassword']}
    # Check all targets first; no partial overwrite or reuse of somebody else's parameters.
    for key in payloads:
        try:
            session.call('ssm', 'get-parameter', '--name', session.config['parameters'][key])
        except AwsError as error:
            if error.code != 'ParameterNotFound':
                raise
        else:
            raise ConfigurationError('Parameter exists; inspect ownership before writing: ' + key)
    for key, value in payloads.items():
        arn = session.config['parameters'][key]
        name = '/' + arn.split(':parameter/', 1)[1]
        with tempfile.TemporaryDirectory(prefix='course-k-ssm-') as folder:
            file = Path(folder) / 'input.json'
            private_json(file, {'Name': name, 'Value': value, 'Type': 'SecureString', 'Tier': 'Standard', 'Overwrite': False})
            session.call('ssm', 'put-parameter', '--cli-input-json', 'file://' + str(file))
    return {'parametersCreated': len(payloads)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--cli')
    parser.add_argument('--apply', action='store_true', help='Permit this command to write AWS resources')
    parser.add_argument('--name', help='Explicit validation-* change-set name')
    parser.add_argument('--stage', choices=['foundation', 'tools', 'runtime'])
    parser.add_argument('--step', choices=['bootstrap', 'migration'])
    parser.add_argument('--secrets-file', type=Path)
    parser.add_argument('command', choices=['plan', 'status', 'prepare', 'inspect', 'execute',
                                           'upload-images', 'put-secrets', 'run-task', 'task-status'])
    args = parser.parse_args()
    config = load_config(args.config)
    if args.command == 'plan':
        print(json.dumps({'region': REGION, 'stack': config['stack'], 'template': str(TEMPLATE),
                          'awsContacted': False, 'stages': ['foundation', 'images/secrets', 'tools', 'bootstrap', 'migration', 'runtime'],
                          'productionReady': False}, indent=2))
        return
    mutating = {'prepare', 'execute', 'upload-images', 'put-secrets', 'run-task'}
    if args.command in mutating and not args.apply:
        parser.error('Writes require --apply; use plan for offline preparation.')
    if args.command in {'prepare', 'inspect', 'execute'} and not re.fullmatch(r'validation-[a-zA-Z0-9-]+', args.name or ''):
        parser.error('An explicit validation-* --name is required.')
    if args.command == 'prepare' and not args.stage:
        parser.error('prepare requires --stage.')
    if args.command in {'run-task', 'task-status'} and not args.step:
        parser.error('An explicit --step is required.')
    if args.command == 'put-secrets' and not args.secrets_file:
        parser.error('put-secrets requires --secrets-file.')
    session = Session(config, args.cli)
    if args.command == 'status':
        stack = session.stack()
        result = {'stack': config['stack'], 'status': stack['StackStatus']}
    elif args.command == 'prepare':
        result = prepare(session, args.stage, args.name)
    elif args.command == 'inspect':
        change = inspect_change(session, args.name)
        result = {'status': change['Status'], 'executionStatus': change.get('ExecutionStatus'),
                  'changes': [{k: x['ResourceChange'].get(k) for k in ('LogicalResourceId', 'Action', 'Replacement')}
                              for x in change.get('Changes', [])]}
    elif args.command == 'execute':
        result = execute(session, args.name)
    elif args.command == 'run-task':
        result = run_task(session, args.step)
    elif args.command == 'task-status':
        completed_task(session, args.step)
        result = {'step': args.step, 'completedSuccessfully': True}
    elif args.command == 'upload-images':
        result = upload_images(session)
    else:
        result = put_secrets(session, args.secrets_file)
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, OSError, KeyError, subprocess.SubprocessError) as error:
        # Never print AWS/Docker response bodies, config values or secret inputs.
        print(safe_diagnostic(error), file=sys.stderr)
        sys.exit(1)
