"""On-demand delivery added to the production stack.

Everything here is created only while EnableOnDemand is true and sits beside
the running Fargate service: CloudFront in front of two Lambda functions, an
Aurora cluster that pauses when idle, and a bucket of published reader files.
Existing resources are reused, not changed, except where noted in
add_on_demand().
"""
import copy
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# AWS-managed CloudFront policies.
CACHING_DISABLED = '4135ea2d-6df8-44a3-9df3-4b5a84be39ad'
ALL_VIEWER_EXCEPT_HOST = 'b689b0a8-53d0-40ab-baf2-68738e2966ac'

# The API serves an article's images only to its owner until it is published.
SESSION_COOKIE = 'coursek_session'
# The database pauses after this long without a connection. Five minutes would
# make every autosave after a short break wait for it to resume.
SECONDS_UNTIL_PAUSE = 1800
# Readers' pages stay at the edge for a day and are purged on publication.
EDGE_CACHE_SECONDS = 86400


def ref(name):
    return {'Ref': name}


def sub(value):
    return {'Fn::Sub': value}


def att(name, key='Arn'):
    return {'Fn::GetAtt': [name, key]}


def is_set(parameter):
    return {'Fn::Not': [{'Fn::Equals': [ref(parameter), '']}]}


def host_of(url):
    """The host of an https URL such as a function URL (https://host/)."""
    return {'Fn::Select': [2, {'Fn::Split': ['/', url]}]}


def assume(service):
    return {'Version': '2012-10-17', 'Statement': [
        {'Effect': 'Allow', 'Principal': {'Service': service}, 'Action': 'sts:AssumeRole'}]}


def policy(name, statements):
    return {'PolicyName': name, 'PolicyDocument': {'Version': '2012-10-17', 'Statement': statements}}


def log_writer(group):
    return {'Effect': 'Allow', 'Action': ['logs:CreateLogStream', 'logs:PutLogEvents'], 'Resource': att(group)}


def when_origin_set(value):
    """A list entry that exists only while the on-demand side has its own origin."""
    return {'Fn::If': ['OnDemandOriginSet', value, ref('AWS::NoValue')]}


# The only additions to running resources: while the on-demand side answers on
# its own host name, the owner signs in there and uploads images from there.
ACCEPTANCE_ORIGIN = when_origin_set(ref('OnDemandOrigin'))
ACCEPTANCE_CALLBACK = when_origin_set(sub('${OnDemandOrigin}/api/auth/callback'))
ACCEPTANCE_LOGOUT = when_origin_set(sub('${OnDemandOrigin}/api/auth/signed-out'))


def parameters():
    arn = r'^$|^arn:aws:ssm:us-west-2:[0-9]{12}:parameter/.+$'
    return {
        'EnableOnDemand': {'Type': 'String', 'Default': 'false', 'AllowedValues': ['false', 'true'],
                           'Description': 'Creates the on-demand network, database and reader bucket beside the running service.'},
        'OnDemandDatabaseUrlParameterArn': {'Type': 'String', 'Default': '', 'AllowedPattern': arn,
                                            'Description': 'SSM SecureString with the application URL of the pausing database. Setting it creates the functions and the distribution.'},
        'OnDemandMigrationUrlParameterArn': {'Type': 'String', 'Default': '', 'AllowedPattern': arn,
                                             'Description': 'SSM SecureString with the schema owner URL of the pausing database. Setting it creates its migration task.'},
        'OnDemandOrigin': {'Type': 'String', 'Default': '', 'AllowedPattern': r'^$|^https://[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$',
                           'Description': 'HTTPS origin readers and the owner use for the on-demand side; the distribution host until the switch.'},
        'OnDemandDistributionId': {'Type': 'String', 'Default': '', 'AllowedPattern': '^$|^[A-Z0-9]{8,20}$',
                                   'Description': 'The distribution created by this stack, passed back so the API may purge it.'},
        'OnDemandCertificateArn': {'Type': 'String', 'Default': '',
                                   'AllowedPattern': r'^$|^arn:aws:acm:us-east-1:[0-9]{12}:certificate/[a-z0-9-]+$',
                                   'Description': 'Issued us-east-1 certificate for PublicOrigin. Setting it moves the blog host name to the distribution.'},
    }


def conditions():
    on = {'Fn::Equals': [ref('EnableOnDemand'), 'true']}
    return {
        'OnDemand': on,
        'OnDemandRuntime': {'Fn::And': [on, is_set('OnDemandDatabaseUrlParameterArn')]},
        'OnDemandMigration': {'Fn::And': [on, is_set('OnDemandMigrationUrlParameterArn')]},
        'OnDemandBootstrap': {'Fn::And': [on, is_set('BootstrapImage')]},
        # The restore task copies the migration task, so it needs both URLs.
        'OnDemandRestore': {'Fn::And': [on, is_set('OnDemandDatabaseUrlParameterArn'), is_set('OnDemandMigrationUrlParameterArn')]},
        'OnDemandOriginSet': is_set('OnDemandOrigin'),
        'OnDemandDistributionKnown': is_set('OnDemandDistributionId'),
        'OnDemandAlias': is_set('OnDemandCertificateArn'),
    }


def network():
    """New dual-stack subnets in the existing VPC. Running subnets are untouched.

    Functions leave through IPv6 only (no NAT, no public address). One-off
    Fargate tasks get a public IPv4 address while they run, so the route table
    also points IPv4 at the existing internet gateway.
    """
    ipv6 = {'Fn::Select': [0, att('Vpc', 'Ipv6CidrBlocks')]}

    def subnet(index, zone):
        return {'Type': 'AWS::EC2::Subnet', 'DependsOn': 'OnDemandIpv6', 'Properties': {
            'VpcId': ref('Vpc'), 'AvailabilityZoneId': ref(zone), 'MapPublicIpOnLaunch': False,
            'CidrBlock': f'10.42.{20 + index}.0/24',
            'Ipv6CidrBlock': {'Fn::Select': [index, {'Fn::Cidr': [ipv6, 2, '64']}]},
        }}

    return {
        'OnDemandIpv6': {'Type': 'AWS::EC2::VPCCidrBlock', 'Properties': {'VpcId': ref('Vpc'), 'AmazonProvidedIpv6CidrBlock': True}},
        'OnDemandSubnet1': subnet(0, 'AvailabilityZone1Id'),
        'OnDemandSubnet2': subnet(1, 'AvailabilityZone2Id'),
        'OnDemandEgress': {'Type': 'AWS::EC2::EgressOnlyInternetGateway', 'Properties': {'VpcId': ref('Vpc')}},
        'OnDemandRoutes': {'Type': 'AWS::EC2::RouteTable', 'Properties': {'VpcId': ref('Vpc')}},
        'OnDemandIpv6Route': {'Type': 'AWS::EC2::Route', 'Properties': {
            'RouteTableId': ref('OnDemandRoutes'), 'DestinationIpv6CidrBlock': '::/0', 'EgressOnlyInternetGatewayId': ref('OnDemandEgress')}},
        'OnDemandIpv4Route': {'Type': 'AWS::EC2::Route', 'DependsOn': 'AttachIgw', 'Properties': {
            'RouteTableId': ref('OnDemandRoutes'), 'DestinationCidrBlock': '0.0.0.0/0', 'GatewayId': ref('Igw')}},
        'OnDemandSubnet1Routes': {'Type': 'AWS::EC2::SubnetRouteTableAssociation', 'Properties': {
            'RouteTableId': ref('OnDemandRoutes'), 'SubnetId': ref('OnDemandSubnet1')}},
        'OnDemandSubnet2Routes': {'Type': 'AWS::EC2::SubnetRouteTableAssociation', 'Properties': {
            'RouteTableId': ref('OnDemandRoutes'), 'SubnetId': ref('OnDemandSubnet2')}},
        # Object storage stays inside the AWS network and costs nothing extra.
        'OnDemandStorageEndpoint': {'Type': 'AWS::EC2::VPCEndpoint', 'Properties': {
            'VpcId': ref('Vpc'), 'VpcEndpointType': 'Gateway', 'ServiceName': sub('com.amazonaws.${AWS::Region}.s3'),
            'RouteTableIds': [ref('OnDemandRoutes')]}},
        'OnDemandFunctionSg': {'Type': 'AWS::EC2::SecurityGroup', 'Properties': {
            'VpcId': ref('Vpc'), 'GroupDescription': 'On-demand API function; outbound only',
            'SecurityGroupEgress': [{'IpProtocol': '-1', 'CidrIp': '0.0.0.0/0'}, {'IpProtocol': '-1', 'CidrIpv6': '::/0'}]}},
        'OnDemandDbSg': {'Type': 'AWS::EC2::SecurityGroup', 'Properties': {
            'VpcId': ref('Vpc'), 'GroupDescription': 'Pausing database; ingress explicitly listed',
            'SecurityGroupEgress': [{'IpProtocol': '-1', 'CidrIp': '0.0.0.0/0'}]}},
        'OnDemandFunctionToDb': {'Type': 'AWS::EC2::SecurityGroupIngress', 'Properties': {
            'GroupId': ref('OnDemandDbSg'), 'SourceSecurityGroupId': ref('OnDemandFunctionSg'),
            'IpProtocol': 'tcp', 'FromPort': 3306, 'ToPort': 3306}},
        # One-off bootstrap, migration and restore tasks keep their security group.
        'OnDemandTasksToDb': {'Type': 'AWS::EC2::SecurityGroupIngress', 'Properties': {
            'GroupId': ref('OnDemandDbSg'), 'SourceSecurityGroupId': ref('ApplicationSg'),
            'IpProtocol': 'tcp', 'FromPort': 3306, 'ToPort': 3306}},
    }


def database():
    """Aurora MySQL that pauses to zero capacity when nothing is connected."""
    return {
        'OnDemandDbSubnets': {'Type': 'AWS::RDS::DBSubnetGroup', 'Properties': {
            'DBSubnetGroupDescription': 'Pausing database subnets', 'SubnetIds': [ref('OnDemandSubnet1'), ref('OnDemandSubnet2')]}},
        'OnDemandCluster': {'Type': 'AWS::RDS::DBCluster', 'DeletionPolicy': 'Snapshot', 'UpdateReplacePolicy': 'Snapshot', 'Properties': {
            'Engine': 'aurora-mysql', 'DatabaseName': 'blog', 'MasterUsername': 'blogowner', 'ManageMasterUserPassword': True,
            'StorageEncrypted': True, 'DeletionProtection': True, 'BackupRetentionPeriod': 7, 'CopyTagsToSnapshot': True,
            'DBSubnetGroupName': ref('OnDemandDbSubnets'), 'VpcSecurityGroupIds': [ref('OnDemandDbSg')],
            'ServerlessV2ScalingConfiguration': {'MinCapacity': 0, 'MaxCapacity': 1, 'SecondsUntilAutoPause': SECONDS_UNTIL_PAUSE},
        }},
        'OnDemandInstance': {'Type': 'AWS::RDS::DBInstance', 'DeletionPolicy': 'Delete', 'UpdateReplacePolicy': 'Delete', 'Properties': {
            'Engine': 'aurora-mysql', 'DBClusterIdentifier': ref('OnDemandCluster'), 'DBInstanceClass': 'db.serverless',
            'PubliclyAccessible': False, 'AutoMinorVersionUpgrade': True}},
    }


def database_tools(resources):
    """Bootstrap and migration tasks for the pausing database.

    They are copies of the running tasks that differ only in the database they
    reach, so the reviewed commands and images are the same.
    """
    def retarget(value, replacements):
        text = copy.deepcopy(value)
        if isinstance(text, dict):
            if text in [old for old, _ in replacements]:
                return next(new for old, new in replacements if old == text)
            return {key: retarget(item, replacements) for key, item in text.items()}
        if isinstance(text, list):
            return [retarget(item, replacements) for item in text]
        return text

    cluster_secret = att('OnDemandCluster', 'MasterUserSecret.SecretArn')
    bootstrap_swaps = [
        (att('Database', 'Endpoint.Address'), att('OnDemandCluster', 'Endpoint.Address')),
        (att('Database', 'MasterUserSecret.SecretArn'), cluster_secret),
        (att('BootstrapExecutionRole'), att('OnDemandBootstrapExecutionRole')),
        (att('MigrationTaskRole'), att('OnDemandToolTaskRole')),
        (sub('${AWS::StackName}-bootstrap'), sub('${AWS::StackName}-on-demand-bootstrap')),
    ]
    migration_swaps = [
        (ref('MigrationUrlParameterArn'), ref('OnDemandMigrationUrlParameterArn')),
        (att('MigrationExecutionRole'), att('OnDemandMigrationExecutionRole')),
        (att('MigrationTaskRole'), att('OnDemandToolTaskRole')),
        (sub('${AWS::StackName}-migration'), sub('${AWS::StackName}-on-demand-migration')),
    ]
    added = {
        'OnDemandToolTaskRole': retarget(resources['MigrationTaskRole'], []),
        'OnDemandBootstrapExecutionRole': retarget(resources['BootstrapExecutionRole'], bootstrap_swaps),
        'OnDemandBootstrapTask': retarget(resources['BootstrapTask'], bootstrap_swaps),
        'OnDemandMigrationExecutionRole': retarget(resources['MigrationExecutionRole'], migration_swaps),
        'OnDemandMigrationTask': retarget(resources['MigrationTask'], migration_swaps),
    }
    # The migration container only needs an origin to start; it serves nothing.
    environment = added['OnDemandMigrationTask']['Properties']['ContainerDefinitions'][0]['Environment']
    for entry in environment:
        if entry['Name'] == 'APP_ORIGIN':
            entry['Value'] = ref('PublicOrigin')
    # Restoring a backup into the pausing database writes rows as the
    # application user and copies images from the archive to the image bucket.
    restore_swaps = [
        (ref('OnDemandMigrationUrlParameterArn'), ref('OnDemandDatabaseUrlParameterArn')),
        (att('OnDemandMigrationExecutionRole'), att('OnDemandRestoreExecutionRole')),
        (att('OnDemandToolTaskRole'), att('OnDemandRestoreTaskRole')),
        (sub('${AWS::StackName}-on-demand-migration'), sub('${AWS::StackName}-on-demand-restore')),
    ]
    restore = retarget(added['OnDemandMigrationTask'], restore_swaps)
    container = restore['Properties']['ContainerDefinitions'][0]
    container['Image'] = ref('GoImage')
    # The operator supplies the backup and names the target server when running it.
    container['Command'] = ['restore']
    container['Environment'].extend([
        {'Name': 'MEDIA_BUCKET', 'Value': ref('ImagesBucket')},
        {'Name': 'BACKUP_BUCKET', 'Value': ref('BackupsBucket')},
        {'Name': 'BACKUP_RETAIN', 'Value': '4'},
    ])
    restore_role = retarget(resources['MigrationTaskRole'], [])
    restore_role['Properties']['Policies'] = [policy('restore-into-production', [
        {'Effect': 'Allow', 'Action': ['s3:GetObject'],
         'Resource': [sub('${BackupsBucket.Arn}/images/*'), sub('${BackupsBucket.Arn}/backups/*')]},
        {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject'], 'Resource': sub('${ImagesBucket.Arn}/images/*')},
        {'Effect': 'Allow', 'Action': ['s3:ListBucket'], 'Resource': [att('ImagesBucket'), att('BackupsBucket')]},
    ])]
    added.update({
        'OnDemandRestoreExecutionRole': retarget(added['OnDemandMigrationExecutionRole'], restore_swaps),
        'OnDemandRestoreTaskRole': restore_role,
        'OnDemandRestoreTask': restore,
    })
    for name in ('OnDemandRestoreExecutionRole', 'OnDemandRestoreTaskRole', 'OnDemandRestoreTask'):
        added[name]['Condition'] = 'OnDemandRestore'
    added['OnDemandToolTaskRole']['Condition'] = 'OnDemand'
    added['OnDemandBootstrapExecutionRole']['Condition'] = 'OnDemandBootstrap'
    added['OnDemandBootstrapTask']['Condition'] = 'OnDemandBootstrap'
    added['OnDemandMigrationExecutionRole']['Condition'] = 'OnDemandMigration'
    added['OnDemandMigrationTask']['Condition'] = 'OnDemandMigration'
    return added


def storage():
    """Published reader files. Private; only the distribution may read them."""
    bucket = att('OnDemandPublicBucket')
    objects = sub('${OnDemandPublicBucket.Arn}/*')
    distribution = sub('arn:${AWS::Partition}:cloudfront::${AWS::AccountId}:distribution/${OnDemandDistribution}')
    edge = {'Service': 'cloudfront.amazonaws.com'}
    return {
        'OnDemandPublicBucket': {'Type': 'AWS::S3::Bucket', 'DeletionPolicy': 'Retain', 'UpdateReplacePolicy': 'Retain', 'Properties': {
            'PublicAccessBlockConfiguration': {'BlockPublicAcls': True, 'IgnorePublicAcls': True, 'BlockPublicPolicy': True, 'RestrictPublicBuckets': True},
            'BucketEncryption': {'ServerSideEncryptionConfiguration': [{'ServerSideEncryptionByDefault': {'SSEAlgorithm': 'AES256'}}]},
        }},
        'OnDemandPublicBucketPolicy': {'Type': 'AWS::S3::BucketPolicy', 'Condition': 'OnDemandRuntime', 'Properties': {
            'Bucket': ref('OnDemandPublicBucket'),
            'PolicyDocument': {'Version': '2012-10-17', 'Statement': [
                {'Effect': 'Deny', 'Principal': '*', 'Action': 's3:*', 'Resource': [bucket, objects],
                 'Condition': {'Bool': {'aws:SecureTransport': 'false'}}},
                {'Effect': 'Allow', 'Principal': edge, 'Action': 's3:GetObject', 'Resource': objects,
                 'Condition': {'StringEquals': {'AWS:SourceArn': distribution}}},
                # Without ListBucket a missing object is reported as denied. The
                # image path relies on "missing" to fall back to the API.
                {'Effect': 'Allow', 'Principal': edge, 'Action': 's3:ListBucket', 'Resource': bucket,
                 'Condition': {'StringEquals': {'AWS:SourceArn': distribution}}},
            ]},
        }},
    }


def functions():
    api_name = sub('${AWS::StackName}-api')
    relay_name = sub('${AWS::StackName}-relay')
    function_arn = 'arn:${AWS::Partition}:lambda:${AWS::Region}:${AWS::AccountId}:function:${AWS::StackName}-'
    origin = {'Fn::If': ['OnDemandOriginSet', ref('OnDemandOrigin'), ref('PublicOrigin')]}
    relay_source = (ROOT / 'infra/relay/relay.py').read_text()
    api_statements = [
        log_writer('GoLogs'),
        # A function in a VPC manages its own network interfaces.
        {'Effect': 'Allow', 'Action': ['ec2:CreateNetworkInterface', 'ec2:DescribeNetworkInterfaces', 'ec2:DescribeSubnets',
                                       'ec2:DeleteNetworkInterface', 'ec2:AssignPrivateIpAddresses', 'ec2:UnassignPrivateIpAddresses'],
         'Resource': '*'},
        {'Effect': 'Allow', 'Action': ['ssm:GetParameters'], 'Resource': [ref('OnDemandDatabaseUrlParameterArn')]},
        {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject', 's3:DeleteObject'],
         'Resource': [sub('${ImagesBucket.Arn}/images/*'), sub('${BackupsBucket.Arn}/images/*'), sub('${BackupsBucket.Arn}/backups/*'),
                      sub('${OnDemandPublicBucket.Arn}/data/*'), sub('${OnDemandPublicBucket.Arn}/api/images/*')]},
        {'Effect': 'Allow', 'Action': ['s3:ListBucket'],
         'Resource': [att('ImagesBucket'), att('BackupsBucket'), att('OnDemandPublicBucket')]},
        # Itself, to carry out pending work, and the relay for IPv4-only media.
        {'Effect': 'Allow', 'Action': ['lambda:InvokeFunction'], 'Resource': [sub(function_arn + 'api'), sub(function_arn + 'relay')]},
        {'Fn::If': ['OnDemandDistributionKnown',
                    {'Effect': 'Allow', 'Action': ['cloudfront:CreateInvalidation'],
                     'Resource': sub('arn:${AWS::Partition}:cloudfront::${AWS::AccountId}:distribution/${OnDemandDistributionId}')},
                    ref('AWS::NoValue')]},
    ]

    def role(statements):
        return {'Type': 'AWS::IAM::Role', 'Condition': 'OnDemandRuntime', 'Properties': {
            'AssumeRolePolicyDocument': assume('lambda.amazonaws.com'), 'PermissionsBoundary': ref('RuntimeBoundaryArn'),
            'Policies': [policy('function', statements)]}}

    def url(function, mode):
        return {'Type': 'AWS::Lambda::Url', 'Condition': 'OnDemandRuntime', 'Properties': {
            'TargetFunctionArn': att(function), 'AuthType': 'AWS_IAM', 'InvokeMode': mode}}

    def from_edge(function):
        # Signed requests from this distribution only; the URLs are not public.
        return {'Type': 'AWS::Lambda::Permission', 'Condition': 'OnDemandRuntime', 'Properties': {
            'FunctionName': ref(function), 'Action': 'lambda:InvokeFunctionUrl', 'Principal': 'cloudfront.amazonaws.com',
            'SourceArn': sub('arn:${AWS::Partition}:cloudfront::${AWS::AccountId}:distribution/${OnDemandDistribution}')}}

    def variables(values):
        return {'Variables': values}

    return {
        'OnDemandApiRole': role(api_statements),
        'OnDemandApiFunction': {'Type': 'AWS::Lambda::Function', 'Condition': 'OnDemandRuntime',
                                'DependsOn': ['OnDemandIpv6Route', 'OnDemandSubnet1Routes', 'OnDemandSubnet2Routes', 'OnDemandInstance'],
                                'Properties': {
            'FunctionName': api_name, 'PackageType': 'Image', 'Code': {'ImageUri': ref('GoImage')}, 'Architectures': ['arm64'],
            'Role': att('OnDemandApiRole'), 'MemorySize': 1024,
            # A work invocation may fetch an external source and take a backup.
            'Timeout': 900,
            'LoggingConfig': {'LogGroup': ref('GoLogs')},
            'VpcConfig': {'SubnetIds': [ref('OnDemandSubnet1'), ref('OnDemandSubnet2')], 'SecurityGroupIds': [ref('OnDemandFunctionSg')],
                          'Ipv6AllowedForDualStack': True},
            'Environment': variables({
                'LISTEN_ADDR': '127.0.0.1:8081', 'AWS_LWA_PORT': '8081', 'AWS_LWA_READINESS_CHECK_PATH': '/api/healthz',
                # The database may need to resume before the server is ready.
                'AWS_LWA_ASYNC_INIT': 'true',
                # Owner-only images of up to 5 MiB exceed a buffered response.
                'AWS_LWA_INVOKE_MODE': 'response_stream',
                'AWS_LWA_PASS_THROUGH_PATH': '/internal/work',
                # The only way out of the VPC is IPv6.
                'AWS_USE_DUALSTACK_ENDPOINT': 'true',
                'APP_ORIGIN': origin,
                'DATABASE_URL_PARAMETER': ref('OnDemandDatabaseUrlParameterArn'),
                'MEDIA_BUCKET': ref('ImagesBucket'), 'BACKUP_BUCKET': ref('BackupsBucket'), 'BACKUP_RETAIN': '4',
                'BACKUP_ENABLED': 'true', 'EXTERNAL_SYNC_ENABLED': 'true',
                'OIDC_ISSUER': sub('https://cognito-idp.${AWS::Region}.amazonaws.com/${OwnerPool}'),
                'OIDC_CLIENT_ID': ref('OwnerClient'), 'OIDC_ADMIN_SUBJECT': ref('AdminSubject'),
                'PUBLIC_BUCKET': ref('OnDemandPublicBucket'),
                'PUBLIC_DISTRIBUTION_ID': ref('OnDemandDistributionId'),
                'ON_DEMAND_FUNCTION': api_name, 'FETCH_RELAY_FUNCTION': relay_name,
            }),
        }},
        'OnDemandApiUrl': url('OnDemandApiFunction', 'RESPONSE_STREAM'),
        'OnDemandApiFromEdge': from_edge('OnDemandApiFunction'),
        'OnDemandWebRole': role([log_writer('NodeLogs')]),
        'OnDemandWebFunction': {'Type': 'AWS::Lambda::Function', 'Condition': 'OnDemandRuntime', 'Properties': {
            'FunctionName': sub('${AWS::StackName}-web'), 'PackageType': 'Image', 'Code': {'ImageUri': ref('NodeImage')},
            'Architectures': ['arm64'], 'Role': att('OnDemandWebRole'), 'MemorySize': 1024, 'Timeout': 30,
            'LoggingConfig': {'LogGroup': ref('NodeLogs')},
            # npm writes to its home directory, which is read-only here.
            'ImageConfig': {'EntryPoint': ['node'], 'Command': ['server.mjs'], 'WorkingDirectory': '/web'},
            'Environment': variables({
                'HOST': '127.0.0.1', 'PORT': '3000', 'AWS_LWA_PORT': '3000', 'AWS_LWA_READINESS_CHECK_PATH': '/healthz',
                'NODE_ENV': 'production', 'PUBLIC_DATA_URL': origin, 'EDGE_CACHE_SECONDS': str(EDGE_CACHE_SECONDS),
            }),
        }},
        'OnDemandWebUrl': url('OnDemandWebFunction', 'BUFFERED'),
        'OnDemandWebFromEdge': from_edge('OnDemandWebFunction'),
        'OnDemandRelayLogs': {'Type': 'AWS::Logs::LogGroup', 'Condition': 'OnDemandRuntime', 'Properties': {'RetentionInDays': 14}},
        'OnDemandRelayRole': role([log_writer('OnDemandRelayLogs')]),
        'OnDemandRelayFunction': {'Type': 'AWS::Lambda::Function', 'Condition': 'OnDemandRuntime', 'Properties': {
            'FunctionName': relay_name, 'Runtime': 'python3.13', 'Handler': 'index.handler', 'Architectures': ['arm64'],
            'Role': att('OnDemandRelayRole'), 'MemorySize': 256, 'Timeout': 30,
            'LoggingConfig': {'LogGroup': ref('OnDemandRelayLogs')}, 'Code': {'ZipFile': relay_source},
        }},
        # A draft's backup falls due at midnight UTC, after the last request of the day.
        'OnDemandDailyWork': {'Type': 'AWS::Events::Rule', 'Condition': 'OnDemandRuntime', 'Properties': {
            'ScheduleExpression': 'cron(10 0 * * ? *)',
            'Targets': [{'Id': 'pending-work', 'Arn': att('OnDemandApiFunction'), 'Input': '{"work":"pending"}'}]}},
        'OnDemandDailyWorkPermission': {'Type': 'AWS::Lambda::Permission', 'Condition': 'OnDemandRuntime', 'Properties': {
            'FunctionName': ref('OnDemandApiFunction'), 'Action': 'lambda:InvokeFunction', 'Principal': 'events.amazonaws.com',
            'SourceArn': att('OnDemandDailyWork')}},
    }


def delivery():
    def access(name, kind):
        return {'Type': 'AWS::CloudFront::OriginAccessControl', 'Condition': 'OnDemandRuntime', 'Properties': {
            'OriginAccessControlConfig': {'Name': sub('${AWS::StackName}-' + name), 'OriginAccessControlOriginType': kind,
                                          'SigningBehavior': 'always', 'SigningProtocol': 'sigv4'}}}

    def function_origin(identifier, function_url, read_seconds):
        return {'Id': identifier, 'DomainName': host_of(att(function_url, 'FunctionUrl')),
                'OriginAccessControlId': ref('OnDemandFunctionAccess'),
                'CustomOriginConfig': {'OriginProtocolPolicy': 'https-only', 'OriginSSLProtocols': ['TLSv1.2'],
                                       'OriginReadTimeout': read_seconds}}

    def behavior(pattern, target, cache, methods, origin_request=None):
        entry = {'TargetOriginId': target, 'ViewerProtocolPolicy': 'redirect-to-https', 'Compress': True,
                 'AllowedMethods': methods, 'CachedMethods': ['GET', 'HEAD'], 'CachePolicyId': cache}
        if pattern:
            entry['PathPattern'] = pattern
        if origin_request:
            entry['OriginRequestPolicyId'] = origin_request
        return entry

    read = ['GET', 'HEAD']
    read_and_ask = ['GET', 'HEAD', 'OPTIONS']
    everything = ['GET', 'HEAD', 'OPTIONS', 'PUT', 'POST', 'PATCH', 'DELETE']
    host = host_of(ref('PublicOrigin'))
    return {
        'OnDemandStorageAccess': access('files', 's3'),
        'OnDemandFunctionAccess': access('functions', 'lambda'),
        # Pages say how long the edge may keep them; without that, nothing is kept.
        'OnDemandPageCache': {'Type': 'AWS::CloudFront::CachePolicy', 'Condition': 'OnDemandRuntime', 'Properties': {'CachePolicyConfig': {
            'Name': sub('${AWS::StackName}-pages'), 'MinTTL': 0, 'DefaultTTL': 0, 'MaxTTL': EDGE_CACHE_SECONDS,
            'ParametersInCacheKeyAndForwardedToOrigin': {
                'EnableAcceptEncodingGzip': True, 'EnableAcceptEncodingBrotli': True,
                'QueryStringsConfig': {'QueryStringBehavior': 'all'},
                'CookiesConfig': {'CookieBehavior': 'none'}, 'HeadersConfig': {'HeaderBehavior': 'none'}}}}},
        # Published images are kept; the owner's session reaches the API so an
        # unpublished image is served to the owner alone and never kept.
        'OnDemandImageCache': {'Type': 'AWS::CloudFront::CachePolicy', 'Condition': 'OnDemandRuntime', 'Properties': {'CachePolicyConfig': {
            'Name': sub('${AWS::StackName}-images'), 'MinTTL': 0, 'DefaultTTL': EDGE_CACHE_SECONDS, 'MaxTTL': EDGE_CACHE_SECONDS,
            'ParametersInCacheKeyAndForwardedToOrigin': {
                'EnableAcceptEncodingGzip': False, 'EnableAcceptEncodingBrotli': False,
                'QueryStringsConfig': {'QueryStringBehavior': 'none'},
                'CookiesConfig': {'CookieBehavior': 'whitelist', 'Cookies': [SESSION_COOKIE]},
                'HeadersConfig': {'HeaderBehavior': 'none'}}}}},
        'OnDemandDistribution': {'Type': 'AWS::CloudFront::Distribution', 'Condition': 'OnDemandRuntime', 'Properties': {'DistributionConfig': {
            'Enabled': True, 'Comment': sub('${AWS::StackName} on-demand delivery'), 'HttpVersion': 'http2and3', 'IPV6Enabled': True,
            'PriceClass': 'PriceClass_200',
            'Aliases': {'Fn::If': ['OnDemandAlias', [host], ref('AWS::NoValue')]},
            'ViewerCertificate': {'Fn::If': ['OnDemandAlias',
                                             {'AcmCertificateArn': ref('OnDemandCertificateArn'), 'SslSupportMethod': 'sni-only',
                                              'MinimumProtocolVersion': 'TLSv1.2_2021'},
                                             {'CloudFrontDefaultCertificate': True}]},
            'Origins': [
                function_origin('web', 'OnDemandWebUrl', 30),
                # The first owner request may wait for the database to resume.
                function_origin('api', 'OnDemandApiUrl', 60),
                {'Id': 'files', 'DomainName': att('OnDemandPublicBucket', 'RegionalDomainName'),
                 'OriginAccessControlId': ref('OnDemandStorageAccess'), 'S3OriginConfig': {'OriginAccessIdentity': ''}},
            ],
            'OriginGroups': {'Quantity': 1, 'Items': [{
                'Id': 'images',
                'FailoverCriteria': {'StatusCodes': {'Quantity': 2, 'Items': [403, 404]}},
                'Members': {'Quantity': 2, 'Items': [{'OriginId': 'files'}, {'OriginId': 'api'}]}}]},
            'DefaultCacheBehavior': behavior(None, 'web', ref('OnDemandPageCache'), read_and_ask),
            'CacheBehaviors': [
                # Published images come from the bucket; anything else is the API's decision.
                behavior('/api/images/*', 'images', ref('OnDemandImageCache'), read),
                behavior('/api/*', 'api', CACHING_DISABLED, everything, ALL_VIEWER_EXCEPT_HOST),
                # A cached list could outlive a purge and be rendered into a page that is then kept.
                behavior('/data/*', 'files', CACHING_DISABLED, read),
            ],
        }}},
    }


def monitoring(resources):
    """The running probe, pointed at the distribution and without the database check.

    The running probe asks /api/readyz, which connects to the database. Here
    that would wake a paused database every fifteen minutes.
    """
    names = {'ProbeRole': 'OnDemandProbeRole', 'ProbeFunction': 'OnDemandProbeFunction', 'ProbeSchedule': 'OnDemandProbeSchedule',
             'ProbePermission': 'OnDemandProbePermission', 'ProbeErrorAlarm': 'OnDemandProbeErrorAlarm',
             'ProbeInvocationAlarm': 'OnDemandProbeInvocationAlarm'}

    def renamed(value):
        if isinstance(value, dict):
            if set(value) == {'Ref'} and value['Ref'] in names:
                return ref(names[value['Ref']])
            if set(value) == {'Fn::GetAtt'} and value['Fn::GetAtt'][0] in names:
                return att(names[value['Fn::GetAtt'][0]], value['Fn::GetAtt'][1])
            return {key: renamed(item) for key, item in value.items()}
        if isinstance(value, list):
            return [renamed(item) for item in value]
        return value

    added = {new: renamed(copy.deepcopy(resources[old])) for old, new in names.items()}
    for resource in added.values():
        resource['Condition'] = 'OnDemandRuntime'
    added['OnDemandProbeFunction']['Properties']['Environment'] = {'Variables': {
        'ENDPOINT': sub('https://${OnDemandDistribution.DomainName}'), 'PROBE_PATHS': '/healthz,/'}}
    return added


def add_on_demand(template):
    """Add the on-demand side to the production template in place."""
    template['Parameters'].update(parameters())
    template['Conditions'].update(conditions())
    resources = template['Resources']
    added = {}
    for group in (network(), database(), storage()):
        for resource in group.values():
            resource.setdefault('Condition', 'OnDemand')
        added.update(group)
    added.update(database_tools(resources))
    added.update(functions())
    added.update(delivery())
    added.update(monitoring(resources))
    clash = set(added) & set(resources)
    if clash:
        raise ValueError(f'On-demand resources collide with existing ones: {sorted(clash)}')
    resources.update(added)

    client = resources['OwnerClient']['Properties']
    client['CallbackURLs'].append(ACCEPTANCE_CALLBACK)
    client['LogoutURLs'].append(ACCEPTANCE_LOGOUT)
    resources['ImagesBucket']['Properties']['CorsConfiguration']['CorsRules'][0]['AllowedOrigins'].append(ACCEPTANCE_ORIGIN)

    template['Outputs'].update({
        'OnDemandDbEndpoint': {'Condition': 'OnDemand', 'Value': att('OnDemandCluster', 'Endpoint.Address')},
        'OnDemandDbAdminSecretArn': {'Condition': 'OnDemand', 'Value': att('OnDemandCluster', 'MasterUserSecret.SecretArn')},
        'OnDemandSubnets': {'Condition': 'OnDemand', 'Value': {'Fn::Join': [',', [ref('OnDemandSubnet1'), ref('OnDemandSubnet2')]]}},
        'OnDemandPublicBucket': {'Condition': 'OnDemand', 'Value': ref('OnDemandPublicBucket')},
        'OnDemandBootstrapTaskDefinition': {'Condition': 'OnDemandBootstrap', 'Value': ref('OnDemandBootstrapTask')},
        'OnDemandMigrationTaskDefinition': {'Condition': 'OnDemandMigration', 'Value': ref('OnDemandMigrationTask')},
        'OnDemandRestoreTaskDefinition': {'Condition': 'OnDemandRestore', 'Value': ref('OnDemandRestoreTask')},
        'OnDemandDistributionId': {'Condition': 'OnDemandRuntime', 'Value': ref('OnDemandDistribution')},
        'OnDemandDistributionDomain': {'Condition': 'OnDemandRuntime', 'Value': att('OnDemandDistribution', 'DomainName')},
        'OnDemandApiFunction': {'Condition': 'OnDemandRuntime', 'Value': ref('OnDemandApiFunction')},
        'OnDemandWebFunction': {'Condition': 'OnDemandRuntime', 'Value': ref('OnDemandWebFunction')},
    })
    return template
