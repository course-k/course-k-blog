#!/usr/bin/env python3
"""Release tested application images to the on-demand functions through the production stack."""
import argparse
import hashlib
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
# Only the special release records this after verifying the actual database.
SCHEMA_STATE = '/course-k/blog-production/applied-schema-digest'

def schema_digest(root=ROOT):
    folder = root / 'backend/internal/mysql'
    files = [folder / 'migrations.go', *sorted((folder / 'migrations').glob('*.sql'))]
    digest = hashlib.sha256()
    for path in files:
        digest.update(str(path.relative_to(root)).encode() + b'\0' + path.read_bytes() + b'\0')
    return digest.hexdigest()


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


# A release changes only the on-demand image parameters. These resources take
# the new image directly; the task definitions are replaced by new revisions.
DIRECT = {'OnDemandApiFunction': 'False', 'OnDemandWebFunction': 'False',
          'OnDemandMigrationTask': 'True', 'OnDemandRestoreTask': 'True'}
IMAGE_TARGETS = {'Code', 'ContainerDefinitions'}
# Resources that CloudFormation lists only because they refer to the functions
# or the distribution; it re-reads those values, which do not change.
REEVALUATED = {'OnDemandApiUrl', 'OnDemandWebUrl', 'OnDemandDailyWork', 'OnDemandDailyWorkPermission',
               'OnDemandDistribution', 'OnDemandProbeFunction', 'OnDemandProbeSchedule', 'OnDemandProbePermission'}
IMAGE_PARAMETERS = {'OnDemandNodeImage', 'OnDemandGoImage'}


def review_changes(changes):
    require(changes, 'Empty application change set')
    names = set()
    for entry in changes:
        change = entry['ResourceChange']
        name = change['LogicalResourceId']
        names.add(name)
        details = change.get('Details', [])
        require(change['Action'] == 'Modify', 'Release would add or remove a resource')
        if name in DIRECT:
            require(change.get('Replacement') == DIRECT[name], 'Unexpected replacement of ' + name)
            require(details and all(d['Target'].get('Name') in IMAGE_TARGETS and (
                d.get('Evaluation') == 'Dynamic' or d.get('CausingEntity') in IMAGE_PARAMETERS) for d in details),
                'Release would change more than the image of ' + name)
        else:
            require(name in REEVALUATED, 'Release would change infrastructure outside the application')
            require(change.get('Replacement') != 'True', 'Unexpected replacement of ' + name)
            require(details and all(d.get('Evaluation') == 'Dynamic' and d.get('ChangeSource') == 'ResourceAttribute'
                                    for d in details), 'Unexpected direct change to ' + name)
    require(names & {'OnDemandApiFunction', 'OnDemandWebFunction'}, 'No application image update in change set')


def wait_for(check, timeout=600, interval=10):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(interval)
    raise ReleaseError('Timed out waiting for AWS; inspect the workflow and stack events')


class AWS:
    def call(self, service, action, *, data=None, raw=False, file_body=None, extra=()):
        command = ['aws', service, action, '--region', REGION, '--no-cli-pager',
                   '--cli-connect-timeout', '10', '--cli-read-timeout', '300' if action == 'invoke' else '45']
        if not raw:
            command += ['--output', 'json']
        if file_body is not None:
            command += ['--body', str(file_body)]
        # Input can contain environment parameter references; never echo it or raw AWS errors.
        if data is not None:
            command += ['--cli-input-json', json.dumps(data)]
        command += list(extra)
        result = subprocess.run(command, capture_output=True, text=True, timeout=330 if action == 'invoke' else 90)
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

    def preflight(self, *, special=False):
        identity = self.aws.call('sts', 'get-caller-identity')
        require(identity['Account'] == self.account and identity['Arn'].startswith(
            f'arn:aws:sts::{self.account}:assumed-role/course-k-blog-github-deploy/'),
            'Wrong AWS deployment identity')
        actual = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        require(actual == self.commit, 'Checkout differs from tested commit')
        if not special:
            state = self.aws.call('ssm', 'get-parameter', data={'Name': SCHEMA_STATE})
            require(state['Parameter']['Type'] == 'String' and state['Parameter']['Value'] == schema_digest(),
                    'Schema state differs; use the reviewed special release workflow')
        stack = self.aws.stack()
        require(stack['StackStatus'] in {'UPDATE_COMPLETE', 'UPDATE_ROLLBACK_COMPLETE'},
                'Production stack is not ready for an update')
        require(f':{self.account}:stack/{STACK}/' in stack['StackId'], 'Unexpected stack account')
        self.outputs = {entry['OutputKey']: entry['OutputValue'] for entry in stack['Outputs']}
        self.previous = {entry['ParameterKey']: entry['ParameterValue'] for entry in stack['Parameters']}
        require(self.previous.get('PublicOrigin') == ORIGIN and self.previous.get('OnDemandOrigin') == '',
                'Unexpected production hostname')
        resources = self.aws.call('cloudformation', 'list-stack-resources', data={'StackName': STACK})
        self.resources = {r['LogicalResourceId']: r['PhysicalResourceId']
                          for r in resources['StackResourceSummaries'] if r.get('PhysicalResourceId')}
        require('ApplicationService' not in self.resources, 'The Fargate service still exists')
        self.functions = {'OnDemandGoImage': self.resources['OnDemandApiFunction'],
                          'OnDemandNodeImage': self.resources['OnDemandWebFunction']}
        self.distribution = self.resources['OnDemandDistribution']
        self.check_functions({key: self.previous[key] for key in IMAGE_PARAMETERS})
        print('AWS identity, current production functions and unchanged database schema verified', flush=True)

    def check_functions(self, images):
        """Both functions run the given images and finished their last update."""
        for parameter, function in self.functions.items():
            result = self.aws.call('lambda', 'get-function', data={'FunctionName': function})
            config = result['Configuration']
            require(config.get('State') == 'Active' and config.get('LastUpdateStatus') == 'Successful',
                    'Function is not ready: ' + function)
            require(result['Code'].get('ImageUri') == images[parameter], 'Function image differs from stack: ' + function)

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
                ('node', 'NodeRepository', 'frontend', 'OnDemandNodeImage'),
                ('go', 'GoRepository', 'backend', 'OnDemandGoImage'),
            ]:
                repository = self.outputs[output]
                image_reference(repository, 'sha256:' + '0' * 64)
                require(repository.startswith(registry + '/'), 'Repository belongs to another account')
                tag = f'commit-{self.commit}'
                target = repository + ':' + tag
                try:
                    existing = self.aws.call('ecr', 'describe-images', data={
                        'repositoryName': repository.split('/', 1)[1], 'imageIds': [{'imageTag': tag}]})['imageDetails'][0]
                except ReleaseError as error:
                    if 'ImageNotFoundException' not in str(error):
                        raise
                else:
                    images[parameter] = image_reference(repository, existing['imageDigest'])
                    print(f'{name}: reusing pinned image for tested commit', flush=True)
                    continue
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
        """Run the pending work once, which takes a backup when content changed."""
        with tempfile.TemporaryDirectory(prefix='blog-invoke-') as directory:
            output = Path(directory) / 'response.json'
            # This command takes its required arguments only as options, not from --cli-input-json.
            result = self.aws.call('lambda', 'invoke', extra=[
                '--function-name', self.functions['OnDemandGoImage'], '--payload', '{"work":"pending"}',
                '--cli-binary-format', 'raw-in-base64-out', str(output)])
            require(result.get('StatusCode') == 200 and not result.get('FunctionError'), 'Pending work failed')
            response = json.loads(output.read_text() or '{}')
            require(response.get('statusCode') == 200, 'Pending work failed; production was not updated')
        print('Pending work, including any due backup, completed successfully', flush=True)

    def change_images(self, images, suffix):
        stack = self.aws.stack()
        require(stack['StackStatus'] in {'UPDATE_COMPLETE', 'UPDATE_ROLLBACK_COMPLETE'},
                'Stack changed before release')
        current = {p['ParameterKey']: p['ParameterValue'] for p in stack['Parameters']}
        if images and all(current.get(key) == image for key, image in images.items()):
            print('Requested images already deployed; skipping empty change set', flush=True)
            return
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
            if status not in {'UPDATE_IN_PROGRESS', 'UPDATE_COMPLETE_CLEANUP_IN_PROGRESS'}:
                # Report only IAM action names, never raw events or secret values.
                events = self.aws.call('cloudformation', 'describe-stack-events',
                                       data={'StackName': STACK})['StackEvents'][:30]
                denied = sorted({action for event in events for action in re.findall(
                    r'not authorized to perform: ([A-Za-z0-9:]+)', event.get('ResourceStatusReason', ''))})
                detail = '; denied actions: ' + ', '.join(denied) if denied else ''
                raise ReleaseError('Application update failed or rolled back; inspect stack events' + detail)
            return None

        wait_for(complete, timeout=900)

    def purge(self):
        """Readers' pages stay at the edge for a day; drop them so they use the new build."""
        result = self.aws.call('cloudfront', 'create-invalidation', data={
            'DistributionId': self.distribution, 'InvalidationBatch': {
                'Paths': {'Quantity': 1, 'Items': ['/*']}, 'CallerReference': f'release-{self.run_id}-{self.attempt}-{time.time_ns()}'}})
        identifier = result['Invalidation']['Id']

        def done():
            value = self.aws.call('cloudfront', 'get-invalidation', data={
                'DistributionId': self.distribution, 'Id': identifier})
            return value['Invalidation']['Status'] == 'Completed'

        wait_for(done, timeout=900, interval=15)

    def verify(self, images):
        # Functions finish updating shortly after the stack reports completion.
        def ready():
            try:
                self.check_functions(images)
            except ReleaseError:
                return None
            return True

        wait_for(ready, timeout=300)
        self.purge()

        def public():
            for path in ('/healthz', '/api/readyz', '/'):
                try:
                    with urllib.request.urlopen(ORIGIN + path, timeout=60) as response:
                        if response.status != 200 or response.geturl() != ORIGIN + path:
                            return None
                        if path == '/' and 'course_k' not in response.read(2_000_000).decode('utf-8'):
                            return None
                except OSError:
                    return None
            return True

        # The first request after a pause waits for the database to resume.
        wait_for(public, timeout=300, interval=15)
        print('Pinned images, updated functions, purged pages and public HTTPS verified', flush=True)

    def apply(self):
        self.preflight()
        images = self.images()
        self.backup()
        self.change_images(images, 'update')
        try:
            self.verify(images)
        except Exception as error:
            print('Post-release verification failed; restoring previous image digests', flush=True)
            previous = {key: self.previous[key] for key in IMAGE_PARAMETERS}
            self.change_images(previous, 'restore')
            self.verify(previous)
            raise ReleaseError('Candidate failed verification; previous images restored') from error
        summary = os.environ.get('GITHUB_STEP_SUMMARY')
        if summary:
            with open(summary, 'a') as stream:
                stream.write(f'Production updated to `{self.commit}`. Pending work, functions and HTTPS passed.\n')


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
