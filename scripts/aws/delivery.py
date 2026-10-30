#!/usr/bin/env python3
"""Release tested application images through the existing production stack."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.request

REGION = 'us-west-2'
STACK = 'course-k-blog-production'
ORIGIN = 'https://blog.course-k.com'
ROOT = Path(__file__).resolve().parents[2]
# Applied schema baseline. Changes require a separately reviewed migration first.
SCHEMA_BASELINE = '672e66ff91ede2cebb45c1869b199a3ecc27f22b'


class ReleaseError(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise ReleaseError(message)


def image_reference(repository, digest):
    require(re.fullmatch(r'[0-9]{12}\.dkr\.ecr\.us-west-2\.amazonaws\.com/'
                         r'course-k-blog-production-(node|go|router)repository-[a-z0-9]+', repository),
            'Unexpected image repository')
    require(re.fullmatch(r'sha256:[a-f0-9]{64}', digest), 'Image must be pinned by digest')
    return repository + '@' + digest


def review_changes(changes):
    require(changes, 'Empty application change set')
    names = set()
    for entry in changes:
        change = entry['ResourceChange']
        name = change['LogicalResourceId']
        names.add(name)
        require(change['Action'] == 'Modify', 'Release would add or remove a resource')
        require(name in {'ApplicationTask', 'ApplicationService', 'MigrationTask',
                         'ApplicationCPUUtilizationAlarm', 'ApplicationMemoryUtilizationAlarm'},
                'Release would change infrastructure outside the application')
        if name == 'ApplicationService':
            require(change.get('Replacement') == 'False', 'Service replacement is forbidden')
        if name.endswith('Alarm'):
            require(change.get('Replacement') == 'False', 'Alarm replacement is forbidden')
            require(all(detail['Target'].get('Name') == 'Dimensions' and
                        detail.get('CausingEntity') == 'ApplicationService.Name'
                        for detail in change.get('Details', [])), 'Unexpected alarm change')
    require('ApplicationTask' in names, 'No application image update in change set')


def validate_service(service, task):
    require(service.get('taskDefinition') == task, 'Service differs from stack task definition')
    require(service.get('desiredCount') == service.get('runningCount') == 1 and
            service.get('pendingCount', 0) == 0, 'Production service is not stable')
    require(len(service.get('deployments', [])) == 1 and
            service['deployments'][0].get('rolloutState') == 'COMPLETED', 'Deployment is not complete')


def wait_for(check, timeout=600, interval=10):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(interval)
    raise ReleaseError('Timed out waiting for AWS; inspect the workflow and stack events')


class AWS:
    def call(self, service, action, *, data=None, raw=False):
        command = ['aws', service, action, '--region', REGION, '--no-cli-pager',
                   '--cli-connect-timeout', '10', '--cli-read-timeout', '45']
        if not raw:
            command += ['--output', 'json']
        # Input can contain environment parameter references; never echo it or raw AWS errors.
        if data is not None:
            command += ['--cli-input-json', json.dumps(data)]
        result = subprocess.run(command, capture_output=True, text=True, timeout=90)
        if result.returncode:
            code = re.search(r'An error occurred \(([A-Za-z0-9_.-]+)\)', result.stderr)
            raise ReleaseError(f'{service} {action} failed ({code[1] if code else "CommandFailed"})')
        return result.stdout.strip() if raw else json.loads(result.stdout or '{}')

    def stack(self):
        result = self.call('cloudformation', 'describe-stacks', data={'StackName': STACK})['Stacks'][0]
        require(result['StackName'] == STACK, 'Wrong stack')
        require(not result.get('RoleARN'), 'Existing CloudFormation service role needs separate review')
        return result


class Release:
    def __init__(self, aws, account, commit):
        self.aws, self.account, self.commit = aws, account, commit
        self.run_id = os.environ.get('GITHUB_RUN_ID', 'local')
        self.attempt = os.environ.get('GITHUB_RUN_ATTEMPT', '1')
        require(re.fullmatch(r'[a-f0-9]{40}', commit), 'Expected a full commit SHA')
        require(re.fullmatch(r'[0-9]{12}', account), 'Expected AWS account ID')
        require(re.fullmatch(r'[a-z0-9-]+', self.run_id + '-' + self.attempt), 'Invalid run identity')

    def preflight(self):
        identity = self.aws.call('sts', 'get-caller-identity')
        require(identity['Account'] == self.account and identity['Arn'].startswith(
            f'arn:aws:sts::{self.account}:assumed-role/course-k-blog-github-deploy/'),
            'Wrong AWS deployment identity')
        actual = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        require(actual == self.commit, 'Checkout differs from tested commit')
        result = subprocess.run(['git', 'diff', '--quiet', SCHEMA_BASELINE, self.commit, '--',
                                 'backend/internal/mysql/migrations', 'backend/internal/mysql/migrations.go'], cwd=ROOT)
        require(result.returncode == 0, 'Database migration changes require a separate release')
        stack = self.aws.stack()
        require(stack['StackStatus'] in {'UPDATE_COMPLETE', 'UPDATE_ROLLBACK_COMPLETE'},
                'Production stack is not ready for an update')
        require(f':{self.account}:stack/{STACK}/' in stack['StackId'], 'Unexpected stack account')
        self.outputs = {entry['OutputKey']: entry['OutputValue'] for entry in stack['Outputs']}
        self.previous = {entry['ParameterKey']: entry['ParameterValue'] for entry in stack['Parameters']}
        require(self.previous['PublicOrigin'] == ORIGIN, 'Unexpected production hostname')
        resources = self.aws.call('cloudformation', 'list-stack-resources', data={'StackName': STACK})
        self.resources = {r['LogicalResourceId']: r['PhysicalResourceId']
                          for r in resources['StackResourceSummaries'] if r.get('PhysicalResourceId')}
        self.cluster = self.resources['Cluster']
        self.service_arn = self.resources['ApplicationService']
        self.old_task = self.resources['ApplicationTask']
        validate_service(self.service(), self.old_task)
        template = self.aws.call('cloudformation', 'get-template', data={'StackName': STACK})['TemplateBody']
        if isinstance(template, str):
            template = json.loads(template)
        require(template['Resources']['ApplicationTask']['Properties']['RuntimePlatform']['CpuArchitecture']
                == 'ARM64', 'Expected ARM64 runtime')
        task = self.running_task(self.old_task)
        expected = {'node': 'NodeImage', 'go': 'GoImage', 'router': 'RouterImage'}
        require({c['name']: c['image'] for c in task['containers']} ==
                {name: self.previous[key] for name, key in expected.items()}, 'Stack and running images differ')
        print('AWS identity, current production deployment and unchanged database schema verified', flush=True)

    def running_task(self, definition):
        # DescribeTaskDefinition has no resource-level IAM scoping. Read only
        # this service's running task instead of granting account-wide reads.
        tasks = self.aws.call('ecs', 'list-tasks', data={
            'cluster': self.cluster, 'serviceName': self.service_arn})['taskArns']
        require(len(tasks) == 1, 'Unexpected number of production tasks')
        result = self.aws.call('ecs', 'describe-tasks', data={'cluster': self.cluster, 'tasks': tasks})
        require(not result.get('failures') and len(result.get('tasks', [])) == 1,
                'Production task cannot be identified')
        task = result['tasks'][0]
        require(task['taskDefinitionArn'] == definition, 'Running task differs from stack definition')
        require(all(c.get('healthStatus') == 'HEALTHY' for c in task['containers']),
                'Production containers are not healthy')
        return task

    def service(self):
        result = self.aws.call('ecs', 'describe-services', data={
            'cluster': self.cluster, 'services': [self.service_arn]})
        require(not result.get('failures') and len(result.get('services', [])) == 1,
                'Cannot identify production service')
        return result['services'][0]

    def images(self):
        images = {}
        registry = f'{self.account}.dkr.ecr.{REGION}.amazonaws.com'
        with tempfile.TemporaryDirectory(prefix='blog-docker-auth-') as directory:
            env = {**os.environ, 'DOCKER_CONFIG': directory}
            password = self.aws.call('ecr', 'get-login-password', raw=True)
            subprocess.run(['docker', 'login', '--username', 'AWS', '--password-stdin', registry],
                           input=password, text=True, check=True, env=env, stdout=subprocess.DEVNULL)
            password = None
            for name, output, folder, parameter in [
                ('node', 'NodeRepository', 'frontend', 'NodeImage'),
                ('go', 'GoRepository', 'backend', 'GoImage'),
                ('router', 'RouterRepository', 'infra/router', 'RouterImage'),
            ]:
                repository = self.outputs[output]
                image_reference(repository, 'sha256:' + '0' * 64)
                require(repository.startswith(registry + '/'), 'Repository belongs to another account')
                tag = f'gh-{self.commit}-{self.run_id}-{self.attempt}'
                target = repository + ':' + tag
                subprocess.run(['docker', 'build', '--platform', 'linux/arm64',
                                '--label', f'org.opencontainers.image.revision={self.commit}',
                                '-t', target, folder], cwd=ROOT, env=env, check=True)
                subprocess.run(['docker', 'push', target], env=env, check=True)
                image = self.aws.call('ecr', 'describe-images', data={
                    'repositoryName': repository.split('/', 1)[1], 'imageIds': [{'imageTag': tag}],
                })['imageDetails'][0]
                images[parameter] = image_reference(repository, image['imageDigest'])
                print(f'{name}: ARM64 image published and pinned by digest', flush=True)
        return images

    def backup(self):
        validate_service(self.service(), self.old_task)
        result = self.aws.call('ecs', 'run-task', data={
            'cluster': self.cluster, 'taskDefinition': self.old_task, 'launchType': 'FARGATE',
            'count': 1, 'networkConfiguration': self.service()['networkConfiguration'],
            'startedBy': 'gh-backup-' + self.run_id,
            'overrides': {'containerOverrides': [{'name': 'go', 'command': ['backup']}]},
        })
        require(not result.get('failures') and len(result.get('tasks', [])) == 1, 'Backup task did not start')
        arn = result['tasks'][0]['taskArn']

        def check():
            result = self.aws.call('ecs', 'describe-tasks', data={'cluster': self.cluster, 'tasks': [arn]})
            require(not result.get('failures') and len(result.get('tasks', [])) == 1, 'Backup task unavailable')
            task = result['tasks'][0]
            go = next(c for c in task['containers'] if c['name'] == 'go')
            if go.get('lastStatus') == 'STOPPED':
                require(go.get('exitCode') == 0, 'Backup command failed; production was not updated')
                return True
            require(task['lastStatus'] != 'STOPPED', 'Backup stopped without successful completion')
            return None

        try:
            wait_for(check, timeout=300)
        finally:
            # Stop only the task created above, never the running service's task.
            self.aws.call('ecs', 'stop-task', data={
                'cluster': self.cluster, 'task': arn, 'reason': 'Pre-release backup finished'})
        print('Pre-release backup command completed successfully', flush=True)

    def change_images(self, images, suffix):
        stack = self.aws.stack()
        require(stack['StackStatus'] in {'UPDATE_COMPLETE', 'UPDATE_ROLLBACK_COMPLETE'},
                'Stack changed before release')
        name = f'{STACK}-release-{self.run_id}-{self.attempt}-{suffix}'
        result = self.aws.call('cloudformation', 'create-change-set', data={
            'StackName': STACK, 'ChangeSetName': name, 'ChangeSetType': 'UPDATE',
            'UsePreviousTemplate': True, 'Capabilities': ['CAPABILITY_NAMED_IAM'],
            'Parameters': [{'ParameterKey': p['ParameterKey'],
                            **({'ParameterValue': images[p['ParameterKey']]} if p['ParameterKey'] in images
                               else {'UsePreviousValue': True})} for p in stack['Parameters']],
            'Description': 'Application images for tested GitHub commit ' + self.commit,
        })
        arn = result['Id']

        def ready():
            value = self.aws.call('cloudformation', 'describe-change-set', data={'ChangeSetName': arn})
            require(value['Status'] != 'FAILED', 'Application change set failed')
            if value['Status'] == 'CREATE_COMPLETE':
                require(not value.get('NextToken'), 'Unexpected paginated release changes')
                review_changes(value.get('Changes', []))
                return value
            return None

        reviewed = wait_for(ready, timeout=300)
        print('Allowed change set: ' + ', '.join(
            c['ResourceChange']['LogicalResourceId'] for c in reviewed['Changes']), flush=True)
        self.aws.call('cloudformation', 'execute-change-set', data={'ChangeSetName': arn})

        def complete():
            value = self.aws.stack()
            status = value['StackStatus']
            if status == 'UPDATE_COMPLETE':
                # ExecuteChangeSet is asynchronous; an immediate read can still
                # return the previous completed stack before the update starts.
                parameters = {p['ParameterKey']: p['ParameterValue'] for p in value['Parameters']}
                return value if all(parameters.get(key) == image for key, image in images.items()) else None
            require(status in {'UPDATE_IN_PROGRESS', 'UPDATE_COMPLETE_CLEANUP_IN_PROGRESS'},
                    'Application update failed or rolled back; inspect stack events')
            return None

        wait_for(complete, timeout=900)

    def verify(self, images):
        stack = self.aws.stack()
        outputs = {o['OutputKey']: o['OutputValue'] for o in stack['Outputs']}
        task = outputs['ApplicationTaskDefinition']
        validate_service(self.service(), task)
        running = self.running_task(task)
        expected = {'node': images['NodeImage'], 'go': images['GoImage'], 'router': images['RouterImage']}
        require({c['name']: c['image'] for c in running['containers']} == expected,
                'Running task definition does not match released images')
        for path in ('/healthz', '/api/readyz', '/'):
            with urllib.request.urlopen(ORIGIN + path, timeout=30) as response:
                require(response.status == 200, 'Public HTTPS verification failed')
                require(response.geturl() == ORIGIN + path, 'Unexpected public redirect')
                if path == '/':
                    require('course_k' in response.read(2_000_000).decode('utf-8'), 'Unexpected homepage')
        print('Pinned images, healthy containers and public HTTPS verified', flush=True)

    def apply(self):
        self.preflight()
        images = self.images()
        self.backup()
        validate_service(self.service(), self.old_task)
        self.change_images(images, 'update')
        try:
            self.verify(images)
        except Exception as error:
            print('Post-release verification failed; restoring previous image digests', flush=True)
            previous = {key: self.previous[key] for key in ('NodeImage', 'GoImage', 'RouterImage')}
            self.change_images(previous, 'restore')
            self.verify(previous)
            raise ReleaseError('Candidate failed verification; previous images restored') from error
        summary = os.environ.get('GITHUB_STEP_SUMMARY')
        if summary:
            with open(summary, 'a') as stream:
                stream.write(f'Production updated to `{self.commit}`. Backup, ECS health and HTTPS passed.\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--account', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    try:
        release = Release(AWS(), args.account, args.commit)
        release.apply() if args.apply else release.preflight()
    except Exception as error:
        # AWS output, environment and task definitions can include private references.
        print(str(error) if isinstance(error, ReleaseError) else
              'Release failed; private command output suppressed. Inspect the failing stage.', flush=True)
        raise SystemExit(1)
