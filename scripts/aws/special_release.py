#!/usr/bin/env python3
"""Explicit, serialized release for compatible schema and image-bucket changes."""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

from delivery import AWS, Release, ReleaseError, ROOT, STACK, REGION, SCHEMA_STATE, require, schema_digest, wait_for


def review_template(actual, target):
    require(set(actual['Resources']) == set(target['Resources']), 'Special release may not add or remove resources')
    for section in ('Conditions', 'Rules', 'Mappings', 'Outputs'):
        old, new = copy.deepcopy(actual.get(section, {})), copy.deepcopy(target.get(section, {}))
        if section == 'Conditions':
            old.pop('MigrationOverride', None); new.pop('MigrationOverride', None)
        require(old == new, 'Unexpected template control change: ' + section)
    expected_condition = {'Fn::Not': [{'Fn::Equals': [{'Ref': 'MigrationImage'}, '']}]}
    require(target['Conditions'].get('MigrationOverride') == expected_condition, 'Unexpected migration override condition')
    for key, value in actual['Parameters'].items():
        if key == 'MigrationImage': continue
        old, new = dict(value), dict(target['Parameters'].get(key, {}))
        old.pop('Description', None); new.pop('Description', None)
        require(old == new, 'Unexpected parameter change: ' + key)
    require(set(target['Parameters']) - set(actual['Parameters']) <= {'MigrationImage'}, 'Unexpected new parameter')
    require(target['Parameters']['MigrationImage']['Type'] == 'String' and target['Parameters']['MigrationImage']['Default'] == '', 'Invalid migration parameter')
    changed = []
    for name, old in actual['Resources'].items():
        new = target['Resources'][name]
        if old == new:
            continue
        a, b = copy.deepcopy(old), copy.deepcopy(new)
        if name == 'MigrationTask':
            a['Properties']['ContainerDefinitions'][0].pop('Image')
            b['Properties']['ContainerDefinitions'][0].pop('Image')
        elif name == 'ImagesBucket':
            for key in ('CorsConfiguration', 'LifecycleConfiguration'):
                a['Properties'].pop(key, None)
                b['Properties'].pop(key, None)
            require(new['Properties'].get('CorsConfiguration') == {'CorsRules': [{
                'AllowedOrigins': [{'Ref': 'PublicOrigin'}], 'AllowedMethods': ['POST']}]}, 'Unexpected CORS rules')
            rules = new['Properties'].get('LifecycleConfiguration', {}).get('Rules', [])
            require(rules and all(r.get('Prefix') == 'images/staging/' and r.get('ExpirationInDays') == 1
                                and r.get('NoncurrentVersionExpiration') == {'NoncurrentDays': 1} for r in rules), 'Unexpected image expiry')
        else:
            raise ReleaseError('Unexpected infrastructure change: ' + name)
        require(a == b, 'Special release would change additional resource properties: ' + name)
        changed.append(name)
    return changed


def review_special_changes(changes):
    # CORS changes cause CloudFormation to reevaluate unchanged references.
    dynamic = {
        'ImagesBucketPolicy': ('PolicyDocument', 'ImagesBucket.Arn'),
        'ApplicationTaskRole': ('Policies', 'ImagesBucket.Arn'),
        'ApplicationTask': ('TaskRoleArn', 'ApplicationTaskRole.Arn'),
        'ApplicationService': ('TaskDefinition', 'ApplicationTask'),
        'ApplicationCPUUtilizationAlarm': ('Dimensions', 'ApplicationService.Name'),
        'ApplicationMemoryUtilizationAlarm': ('Dimensions', 'ApplicationService.Name'),
    }
    for item in changes:
        c = item['ResourceChange']; name = c['LogicalResourceId']
        require(c['Action'] == 'Modify', 'Special release would add or remove infrastructure')
        if name == 'MigrationTask':
            require(all(d['Target']['Name'] in {'ContainerDefinitions', 'Image'} for d in c.get('Details', [])), 'Unexpected migration task change')
        elif name == 'ImagesBucket':
            require(c.get('Replacement') == 'False', 'Image bucket replacement forbidden')
            require(all(d['Target']['Name'] in {'CorsConfiguration', 'LifecycleConfiguration'} for d in c.get('Details', [])), 'Unexpected bucket property')
        else:
            require(name in dynamic, 'Unexpected dependent infrastructure')
            prop, cause = dynamic[name]
            require(all(d.get('Evaluation') == 'Dynamic' and d['Target']['Name'] == prop
                        and d.get('CausingEntity') == cause for d in c.get('Details', [])), 'Unexpected direct infrastructure change')
            require(c.get('Replacement') == ('Conditional' if name == 'ApplicationTask' else 'False'), 'Dependent replacement forbidden')


class SpecialRelease(Release):
    def plan(self):
        self.preflight(special=True)
        actual = self.aws.call('cloudformation', 'get-template', data={'StackName': STACK})['TemplateBody']
        actual = json.loads(actual) if isinstance(actual, str) else actual
        if 'ExternalBackfillTask' in actual['Resources']:
            from backfill import backfill_template
            target = backfill_template()
        elif 'RestoreDatabase' in actual['Resources']:
            from recovery import recovery_template
            target = recovery_template()
        else:
            from production import production_template
            target = production_template()
        for key in ('EnableRestoreExercise', 'EnableRestoreTasks', 'EnableExternalBackfill'):
            require(self.previous.get(key, 'false') == 'false', 'Temporary maintenance operation is active')
        changed = review_template(actual, target)
        self.actual = actual
        self.target = target
        expected = max(int(p.name.split('_', 1)[0]) for p in (ROOT / 'backend/internal/mysql/migrations').glob('*.sql'))
        result = {'commit': self.commit, 'schemaDigest': schema_digest(), 'expectedSchema': expected,
                  'resourceDefinitionsChanged': changed, 'migrationCompatibilityRequiresReview': True}
        print(json.dumps(result, indent=2), flush=True)
        return result

    def prepare_migration(self, image, *, storage=False):
        template = copy.deepcopy(self.target)
        if not storage:
            template['Resources']['ImagesBucket'] = self.actual['Resources']['ImagesBucket']
        current = self.aws.call('cloudformation', 'get-template', data={'StackName': STACK})['TemplateBody']
        current = json.loads(current) if isinstance(current, str) else current
        stack = self.aws.stack()
        current_parameters = {p['ParameterKey']: p['ParameterValue'] for p in stack['Parameters']}
        previous_update = stack.get('LastUpdatedTime')
        if current == template and current_parameters.get('MigrationImage') == image:
            print('Migration/settings preparation already applied; skipping', flush=True)
            return
        body = json.dumps(template, separators=(',', ':')).encode()
        require(len(body) <= 1024 * 1024, 'Template exceeds S3 CloudFormation quota')
        bucket = f'course-k-blog-release-{self.account}-{REGION}'
        key = f'templates/{self.commit}/{hashlib.sha256(body).hexdigest()}.json'
        with tempfile.TemporaryDirectory(prefix='blog-template-') as folder:
            path = Path(folder) / 'template.json'; path.write_bytes(body)
            self.aws.call('s3api', 'put-object', data={'Bucket': bucket, 'Key': key,
                'ServerSideEncryption': 'AES256', 'ContentType': 'application/json'}, file_body=path)
        stack = self.aws.stack()
        previous = {p['ParameterKey'] for p in stack['Parameters']}
        params = []
        for name in template['Parameters']:
            if name == 'MigrationImage':
                params.append({'ParameterKey': name, 'ParameterValue': image})
            elif name in previous:
                params.append({'ParameterKey': name, 'UsePreviousValue': True})
            else:
                require('Default' in template['Parameters'][name], 'New parameter needs explicit review: ' + name)
        x = self.aws.call('cloudformation', 'create-change-set', data={
            'StackName': STACK, 'ChangeSetName': f'{STACK}-release-{self.run_id}-{self.attempt}-' + ('settings' if storage else 'schema'),
            'ChangeSetType': 'UPDATE', 'TemplateURL': f'https://{bucket}.s3.{REGION}.amazonaws.com/{key}',
            'Capabilities': ['CAPABILITY_IAM'], 'Parameters': params})
        arn = x['Id']
        def ready():
            value = self.aws.call('cloudformation', 'describe-change-set', data={'ChangeSetName': arn})
            require(value['Status'] != 'FAILED', 'Special release change set failed')
            if value['Status'] == 'CREATE_COMPLETE':
                require(not value.get('NextToken'), 'Unexpected paginated changes')
                review_special_changes(value.get('Changes', [])); return value
        wait_for(ready, timeout=300)
        self.aws.call('cloudformation', 'execute-change-set', data={'ChangeSetName': arn})
        def complete():
            s = self.aws.stack()
            if s['StackStatus'] == 'UPDATE_COMPLETE':
                p = {x['ParameterKey']: x['ParameterValue'] for x in s['Parameters']}
                if p.get('MigrationImage') != image or s.get('LastUpdatedTime') == previous_update:
                    return None
                applied = self.aws.call('cloudformation', 'get-template', data={'StackName': STACK})['TemplateBody']
                applied = json.loads(applied) if isinstance(applied, str) else applied
                return s if applied == template else None
            require(s['StackStatus'] in {'UPDATE_IN_PROGRESS', 'UPDATE_COMPLETE_CLEANUP_IN_PROGRESS'}, 'Special infrastructure release failed')
        wait_for(complete, timeout=900)

    def special_apply(self, *, digest, expected_schema, record_existing, compatible, sync):
        plan = self.plan()
        require(digest == plan['schemaDigest'] and expected_schema == plan['expectedSchema'], 'Approved schema plan differs from checkout')
        require(record_existing or compatible, 'Explicit backward-compatible migration confirmation required')
        images = self.images()
        self.backup()
        self.prepare_migration(images['GoImage'])
        if not record_existing:
            self.run_command(['migrate'], migration=True)
        # This checks SQL checksums as well as version; record-existing never runs DDL.
        self.run_command(['verify-schema'], migration=True)
        self.prepare_migration(images['GoImage'], storage=True)
        self.aws.call('ssm', 'put-parameter', data={'Name': SCHEMA_STATE, 'Value': digest,
            'Type': 'String', 'Overwrite': True})
        candidate = dict(images, MigrationImage='')
        self.change_images(candidate, 'update')
        try:
            self.verify(images)
            self.production_check(sync=sync)
        except Exception:
            previous = {k: self.previous[k] for k in ('GoImage', 'NodeImage', 'RouterImage')}
            self.change_images(dict(previous, MigrationImage=''), 'restore')
            self.verify(previous)
            raise ReleaseError('Special release acceptance failed; old app restored, schema was not rolled back')
        print('Special release complete; schema state recorded and real production checks passed', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--account', required=True); parser.add_argument('--commit', required=True)
    parser.add_argument('--mode', choices=['plan', 'apply', 'record-existing'], default='plan')
    parser.add_argument('--schema-digest', default=''); parser.add_argument('--expected-schema', type=int, default=0)
    parser.add_argument('--backward-compatible', action='store_true'); parser.add_argument('--check-sync', action='store_true')
    args = parser.parse_args()
    try:
        release = SpecialRelease(AWS(), args.account, args.commit)
        if args.mode == 'plan': release.plan()
        else: release.special_apply(digest=args.schema_digest, expected_schema=args.expected_schema,
            record_existing=args.mode == 'record-existing', compatible=args.backward_compatible, sync=args.check_sync)
    except Exception as error:
        print(str(error) if isinstance(error, ReleaseError) else 'Special release failed; private output suppressed', flush=True)
        raise SystemExit(1)
