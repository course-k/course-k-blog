#!/usr/bin/env python3
"""Explicit, serialized release for a compatible schema change on the pausing database."""
import argparse
import json

from delivery import AWS, IMAGE_PARAMETERS, Release, ReleaseError, ROOT, SCHEMA_STATE, require, schema_digest, wait_for

MIGRATION_FAMILY = 'course-k-blog-production-on-demand-migration'
# Copied from the stack's revision when the candidate image is registered.
COPIED = ('containerDefinitions', 'taskRoleArn', 'executionRoleArn', 'networkMode', 'requiresCompatibilities',
          'cpu', 'memory', 'runtimePlatform', 'volumes')


def candidate_definition(current, image):
    """The stack's migration definition with only the container image replaced."""
    require(current['family'] == MIGRATION_FAMILY, 'Unexpected migration task family')
    definition = {key: json.loads(json.dumps(current[key])) for key in COPIED if key in current}
    containers = definition['containerDefinitions']
    require(len(containers) == 1 and containers[0]['name'] == 'go', 'Unexpected migration containers')
    containers[0]['image'] = image
    return {'family': MIGRATION_FAMILY, **definition}


class SpecialRelease(Release):
    def plan(self):
        self.preflight(special=True)
        expected = max(int(p.name.split('_', 1)[0]) for p in (ROOT / 'backend/internal/mysql/migrations').glob('*.sql'))
        result = {'commit': self.commit, 'schemaDigest': schema_digest(), 'expectedSchema': expected,
                  'migrationCompatibilityRequiresReview': True}
        print(json.dumps(result, indent=2), flush=True)
        return result

    def register_candidate(self, image):
        """A new revision of the migration family that runs the candidate image.

        The task definition takes its image from OnDemandGoImage, which also
        updates the API function. The schema must change before the new code
        serves requests, so the migration runs from this separate revision.
        """
        current = self.aws.call('ecs', 'describe-task-definition', data={
            'taskDefinition': self.resources['OnDemandMigrationTask']})['taskDefinition']
        registered = self.aws.call('ecs', 'register-task-definition', data=candidate_definition(current, image))
        return registered['taskDefinition']['taskDefinitionArn']

    def run_migration(self, definition, command):
        network = {'awsvpcConfiguration': {
            'subnets': [self.resources['OnDemandSubnet1'], self.resources['OnDemandSubnet2']],
            'securityGroups': [self.resources['ApplicationSg']], 'assignPublicIp': 'ENABLED'}}
        cluster = self.resources['Cluster']
        result = self.aws.call('ecs', 'run-task', data={
            'cluster': cluster, 'taskDefinition': definition, 'launchType': 'FARGATE', 'count': 1,
            'networkConfiguration': network, 'startedBy': 'gh-schema-' + self.run_id,
            'overrides': {'containerOverrides': [{'name': 'go', 'command': command}]}})
        require(not result.get('failures') and len(result.get('tasks', [])) == 1, 'Migration task did not start')
        arn = result['tasks'][0]['taskArn']

        def check():
            tasks = self.aws.call('ecs', 'describe-tasks', data={'cluster': cluster, 'tasks': [arn]})
            require(not tasks.get('failures') and len(tasks.get('tasks', [])) == 1, 'Migration task unavailable')
            task = tasks['tasks'][0]
            go = next(c for c in task['containers'] if c['name'] == 'go')
            if go.get('lastStatus') == 'STOPPED':
                require(go.get('exitCode') == 0, 'Migration command failed: ' + ' '.join(command))
                return True
            require(task['lastStatus'] != 'STOPPED', 'Migration task stopped before the command finished')
            return None

        # The first connection may wait for the paused database to resume.
        wait_for(check, timeout=600)
        print('Migration command succeeded: ' + ' '.join(command), flush=True)

    def special_apply(self, *, digest, expected_schema, record_existing, compatible):
        plan = self.plan()
        require(digest == plan['schemaDigest'] and expected_schema == plan['expectedSchema'], 'Approved schema plan differs from checkout')
        require(record_existing or compatible, 'Explicit backward-compatible migration confirmation required')
        images = self.images()
        self.backup()
        definition = self.register_candidate(images['OnDemandGoImage'])
        if not record_existing:
            self.run_migration(definition, ['migrate'])
        # This checks SQL checksums as well as version; record-existing never runs DDL.
        self.run_migration(definition, ['verify-schema'])
        self.aws.call('ssm', 'put-parameter', data={'Name': SCHEMA_STATE, 'Value': digest, 'Type': 'String', 'Overwrite': True})
        self.change_images(images, 'update')
        try:
            self.verify(images)
        except Exception as error:
            previous = {key: self.previous[key] for key in IMAGE_PARAMETERS}
            self.change_images(previous, 'restore')
            self.verify(previous)
            raise ReleaseError('Special release acceptance failed; old app restored, schema was not rolled back') from error
        print('Special release complete; schema state recorded and public checks passed', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--account', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--mode', choices=['plan', 'apply', 'record-existing'], default='plan')
    parser.add_argument('--schema-digest', default='')
    parser.add_argument('--expected-schema', type=int, default=0)
    parser.add_argument('--backward-compatible', action='store_true')
    args = parser.parse_args()
    try:
        release = SpecialRelease(AWS(), args.account, args.commit)
        if args.mode == 'plan':
            release.plan()
        else:
            release.special_apply(digest=args.schema_digest, expected_schema=args.expected_schema,
                                  record_existing=args.mode == 'record-existing', compatible=args.backward_compatible)
    except Exception as error:
        print(str(error) if isinstance(error, ReleaseError) else 'Special release failed; private output suppressed', flush=True)
        raise SystemExit(1)
