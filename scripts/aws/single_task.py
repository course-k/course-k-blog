"""Build the production runtime as one service; keep the retired validation topology intact."""
import copy


def ref(name):
    return {'Ref': name}


def attribute(name, key='Arn'):
    return {'Fn::GetAtt': [name, key]}


def consolidate_runtime(template):
    resources = template['Resources']
    template['Parameters']['RouterImage'] = copy.deepcopy(template['Parameters']['NodeImage'])
    template['Parameters']['RouterImage']['Description'] = 'ARM64 image built from infra/router, pinned by ECR digest; required before runtime activation.'
    template['Rules']['RuntimeRequiresImages'] = {
        'RuleCondition': {'Fn::Equals': [ref('EnableRuntime'), 'true']},
        'Assertions': [{'Assert': {'Fn::Not': [{'Fn::Equals': [ref(name), '']}]},
                        'AssertDescription': f'{name} must be pinned before runtime activation.'}
                       for name in ['NodeImage', 'GoImage', 'RouterImage']],
    }
    resources['RouterRepository'] = copy.deepcopy(resources['NodeRepository'])
    resources['RouterLogs'] = copy.deepcopy(resources['NodeLogs'])

    # One task role is available to every container. This is an accepted boundary,
    # not an attempt to keep Node isolated from Go's S3 permissions.
    resources['ApplicationTaskRole'] = resources.pop('GoTaskRole')
    resources.pop('NodeTaskRole')
    execution = resources.pop('GoExecutionRole')
    statements = execution['Properties']['Policies'][0]['PolicyDocument']['Statement']
    for statement in statements:
        if 'ecr:BatchGetImage' in statement['Action']:
            statement['Resource'] = [attribute(name) for name in ['NodeRepository', 'GoRepository', 'RouterRepository']]
        elif 'logs:PutLogEvents' in statement['Action']:
            statement['Resource'] = [attribute(name) for name in ['NodeLogs', 'GoLogs', 'RouterLogs']]
    resources['ApplicationExecutionRole'] = execution
    resources.pop('NodeExecutionRole')

    node = resources['NodeTask']['Properties']['ContainerDefinitions'][0]
    go = resources['GoTask']['Properties']['ContainerDefinitions'][0]
    for entry in node['Environment']:
        if entry['Name'] == 'API_BASE_URL':
            entry['Value'] = 'http://127.0.0.1:8081'
        elif entry['Name'] == 'HOST':
            entry['Value'] = '127.0.0.1'
    for entry in go['Environment']:
        if entry['Name'] == 'LISTEN_ADDR':
            entry['Value'] = '127.0.0.1:8081'
    # Shares apply only when containers compete; they are not separate CPU caps.
    node.update(Cpu=300, MemoryReservation=512)
    go.update(Cpu=150, MemoryReservation=512)
    router = {
        'Name': 'router', 'Image': ref('RouterImage'), 'Essential': True,
        'Cpu': 30, 'MemoryReservation': 64, 'ReadonlyRootFilesystem': True,
        'LinuxParameters': {'Capabilities': {'Drop': ['ALL']}},
        'MountPoints': [{'SourceVolume': 'router-tmp', 'ContainerPath': '/tmp/nginx'}],
        'PortMappings': [{'ContainerPort': 8080, 'Protocol': 'tcp'}],
        'DependsOn': [{'ContainerName': name, 'Condition': 'HEALTHY'} for name in ['node', 'go']],
        'HealthCheck': {'Command': ['CMD-SHELL', 'wget -q -O /dev/null http://127.0.0.1:8080/gateway-healthz'],
                        'Interval': 30, 'Timeout': 5, 'Retries': 3, 'StartPeriod': 30},
        'LogConfiguration': {'LogDriver': 'awslogs', 'Options': {
            'awslogs-group': ref('RouterLogs'), 'awslogs-region': ref('AWS::Region'), 'awslogs-stream-prefix': 'router',
        }},
    }
    task = resources.pop('NodeTask')
    resources.pop('GoTask')
    task['Properties'].update(
        Family={'Fn::Sub': '${AWS::StackName}-application'}, Cpu='512', Memory='2048',
        ExecutionRoleArn=attribute('ApplicationExecutionRole'), TaskRoleArn=attribute('ApplicationTaskRole'),
        ContainerDefinitions=[node, go, router], Volumes=[{'Name': 'router-tmp'}],
    )
    resources['ApplicationTask'] = task

    resources['ApplicationSg'] = resources.pop('GoSg')
    resources['ApplicationSg']['Properties']['GroupDescription'] = 'Shared application task; only VPC Link may reach the router'
    resources.pop('NodeSg')
    for name in ['LinkToNode', 'LinkToGo', 'NodeToGo']:
        resources.pop(name)
    resources['LinkToApplication'] = {
        'Type': 'AWS::EC2::SecurityGroupIngress', 'Properties': {
            'GroupId': ref('ApplicationSg'), 'SourceSecurityGroupId': ref('LinkSg'),
            'IpProtocol': 'tcp', 'FromPort': 8080, 'ToPort': 8080,
        },
    }
    resources['ApplicationToDb'] = resources.pop('GoToDb')
    resources['ApplicationToDb']['Properties']['SourceSecurityGroupId'] = ref('ApplicationSg')

    discovery = resources.pop('NodeDiscovery')
    resources.pop('GoDiscovery')
    discovery['Properties']['Name'] = 'app'
    resources['ApplicationDiscovery'] = discovery
    service = resources.pop('GoService')
    resources.pop('NodeService')
    service['Properties']['TaskDefinition'] = ref('ApplicationTask')
    service['Properties']['NetworkConfiguration']['AwsvpcConfiguration']['SecurityGroups'] = [ref('ApplicationSg')]
    # ECS permits one registry per service. Register the router once rather than
    # add custom code to track each task's changing address for two ports.
    service['Properties']['ServiceRegistries'] = [{
        'RegistryArn': attribute('ApplicationDiscovery'), 'ContainerName': 'router', 'ContainerPort': 8080,
    }]
    resources['ApplicationService'] = service
    integration = resources.pop('NodeIntegration')
    resources.pop('GoIntegration')
    integration['Properties']['IntegrationUri'] = attribute('ApplicationDiscovery')
    resources['ApplicationIntegration'] = integration
    resources.pop('ApiRoot')
    resources.pop('ApiChildren')
    resources['Screens']['Properties']['Target'] = {'Fn::Join': ['/', ['integrations', ref('ApplicationIntegration')]]}

    # ECS reports CPU/memory per service. Do not create duplicate alarms for one metric.
    for metric in ['CPUUtilization', 'MemoryUtilization']:
        alarm = resources.pop(f'Node{metric}Alarm')
        resources.pop(f'Go{metric}Alarm')
        alarm['Properties']['Dimensions'][1]['Value'] = attribute('ApplicationService', 'Name')
        resources[f'Application{metric}Alarm'] = alarm
    template['Outputs']['RouterRepository'] = {'Value': attribute('RouterRepository', 'RepositoryUri')}
    template['Outputs']['ApplicationTaskDefinition'] = {'Condition': 'RunApplication', 'Value': ref('ApplicationTask')}
    template['Outputs']['ApplicationSecurityGroup'] = {'Value': ref('ApplicationSg')}
