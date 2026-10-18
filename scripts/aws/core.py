"""Shared validation configuration and isolated, short-lived AWS sessions."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]
REGION = 'us-west-2'
TEMPLATE = ROOT / 'infra/aws/validation-stack.template.json'
STATE = ROOT / 'config/aws-validation'


def private_json(path, value):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    # Create/truncate with private permissions, including on an existing file.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')


def cli_environment():
    env = os.environ.copy()
    for name in ('AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_SESSION_TOKEN',
                 'AWS_SECURITY_TOKEN', 'AWS_PROFILE', 'AWS_DEFAULT_PROFILE',
                 'AWS_ROLE_ARN', 'AWS_WEB_IDENTITY_TOKEN_FILE', 'AWS_ENDPOINT_URL'):
        env.pop(name, None)
    for name in list(env):
        if name.startswith('AWS_ENDPOINT_URL_'):
            env.pop(name)
    env.update(AWS_CONFIG_FILE=str(STATE / 'config'),
               AWS_SHARED_CREDENTIALS_FILE=str(STATE / 'credentials'),
               AWS_LOGIN_CACHE_DIRECTORY=str(STATE / 'login-cache'), AWS_PAGER='')
    return env


class ConfigurationError(ValueError):
    """An actionable message authored by this tool; never include input values."""


def safe_diagnostic(error):
    if isinstance(error, (AwsError, ConfigurationError)):
        return str(error)
    if isinstance(error, json.JSONDecodeError):
        return 'Configuration JSON is invalid; check its syntax. Raw content suppressed.'
    return 'Command failed. Check prerequisites and the runbook; raw output suppressed.'


def load_config(path):
    config = json.loads(Path(path).read_text())
    allowed = {'account', 'stack', 'profile', 'parameters', 'images'}
    if set(config) - allowed:
        raise ConfigurationError('Unknown environment field; credentials do not belong in this file.')
    if not re.fullmatch(r'\d{12}', config.get('account', '')):
        raise ConfigurationError('account must be a 12-digit AWS account.')
    if not re.fullmatch(r'course-k-blog-validation-[a-zA-Z0-9-]+', config.get('stack', '')) or len(config['stack']) > 128:
        raise ConfigurationError('Only explicitly named course-k-blog-validation-* stacks are supported.')
    if not re.fullmatch(r'[a-zA-Z0-9_-]+', config.get('profile', '')):
        raise ConfigurationError('An explicit isolated source profile is required.')
    schema = json.loads(TEMPLATE.read_text())['Parameters']
    params = config.get('parameters', {})
    if not isinstance(params, dict) or set(params) - set(schema):
        raise ConfigurationError('Unknown stack parameter.')
    for key, spec in schema.items():
        value = params.get(key, spec.get('Default'))
        if not isinstance(value, str):
            raise ConfigurationError('Missing string parameter: ' + key)
        if spec.get('AllowedValues') and value not in spec['AllowedValues']:
            raise ConfigurationError('Invalid parameter choice: ' + key)
        if spec.get('AllowedPattern') and not re.fullmatch(spec['AllowedPattern'], value):
            raise ConfigurationError('Invalid parameter format: ' + key)
        if key.endswith('ParameterArn'):
            expected = f'arn:aws:ssm:{REGION}:{config["account"]}:parameter/course-k/blog-validation/'
            if not value.startswith(expected):
                raise ConfigurationError('SSM ARN must belong to this account and validation prefix: ' + key)
        if key == 'RuntimeBoundaryArn' and not value.startswith(f'arn:aws:iam::{config["account"]}:policy/'):
            raise ConfigurationError('Runtime boundary belongs to a different account.')
    if params.get('EnableRuntime', 'false') != 'false' or params.get('EnableDatabaseTools', 'false') != 'false':
        raise ConfigurationError('Environment base must keep runtime and database tools disabled.')
    if params.get('AvailabilityZone1Id', 'usw2-az1') == params.get('AvailabilityZone2Id', 'usw2-az2'):
        raise ConfigurationError('Two distinct availability zones are required.')
    images = config.get('images', {})
    if set(images) != {'NodeRepository', 'GoRepository', 'BootstrapRepository'}:
        raise ConfigurationError('Three explicit local Docker image references are required.')
    if any(not isinstance(v, str) or not v or v.startswith('-') for v in images.values()):
        raise ConfigurationError('Invalid local image reference.')
    return config


class AwsError(RuntimeError):
    def __init__(self, service, action, stderr=''):
        # Report the AWS error code only. Raw responses can include secret values.
        match = re.search(r'An error occurred \(([A-Za-z0-9_.-]+)\)', stderr)
        self.code = match.group(1) if match else 'CommandFailed'
        super().__init__(f'{service} {action} failed ({self.code}); raw output suppressed.')


class Session:
    def __init__(self, config, cli=None):
        if config['account'] == '000000000000' or not re.fullmatch(r'17\.\d+', config['parameters'].get('PostgresVersion', '')):
            raise ConfigurationError('Replace account and PostgreSQL placeholders before contacting AWS.')
        self.config = config
        self.cli = cli or os.getenv('AWS_VALIDATION_CLI') or shutil.which('aws')
        if not self.cli:
            raise ConfigurationError('AWS CLI is required; specify --cli or AWS_VALIDATION_CLI.')
        self.env = cli_environment()
        version = subprocess.run([self.cli, '--version'], env=self.env, capture_output=True, text=True)
        match = re.search(r'aws-cli/(\d+)\.(\d+)\.(\d+)', version.stdout + version.stderr)
        if version.returncode or not match or tuple(map(int, match.groups())) < (2, 32, 0):
            raise ConfigurationError('AWS CLI 2.32.0+ is required for browser login.')
        role = f'arn:aws:iam::{config["account"]}:role/course-k-validation-operator'
        result = self._run(['sts', 'assume-role', '--role-arn', role,
                            '--role-session-name', 'blog-validation', '--profile', config['profile']])
        credentials = result['Credentials']
        self.env.update(AWS_ACCESS_KEY_ID=credentials['AccessKeyId'],
                        AWS_SECRET_ACCESS_KEY=credentials['SecretAccessKey'],
                        AWS_SESSION_TOKEN=credentials['SessionToken'])
        identity = self.call('sts', 'get-caller-identity')
        if identity.get('Account') != config['account'] or not identity.get('Arn', '').startswith(
                f'arn:aws:sts::{config["account"]}:assumed-role/course-k-validation-operator/'):
            raise ConfigurationError('Unexpected account or operator role; stopped.')

    def _run(self, args):
        result = subprocess.run([self.cli, *args, '--region', REGION, '--output', 'json',
                                 '--no-cli-pager', '--cli-connect-timeout', '10', '--cli-read-timeout', '45'],
                                env=self.env, capture_output=True, text=True)
        if result.returncode:
            raise AwsError(args[0], args[1], result.stderr)
        return json.loads(result.stdout) if result.stdout.strip() else {}

    def call(self, *args):
        return self._run(list(args))

    def stack(self):
        stack = self.call('cloudformation', 'describe-stacks', '--stack-name', self.config['stack'])['Stacks'][0]
        if stack['StackName'] != self.config['stack']:
            raise ConfigurationError('Unexpected stack.')
        arn = stack.get('StackId', '')
        if not arn.startswith(f'arn:aws:cloudformation:{REGION}:{self.config["account"]}:stack/{self.config["stack"]}/'):
            raise ConfigurationError('Stack account or region mismatch.')
        return stack

    def resources(self):
        self.stack()
        return {r['LogicalResourceId']: r['PhysicalResourceId'] for r in self.call(
            'cloudformation', 'list-stack-resources', '--stack-name', self.config['stack'])['StackResourceSummaries']
            if r.get('PhysicalResourceId')}
