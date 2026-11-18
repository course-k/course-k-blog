from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock

from delivery import ReleaseError, schema_digest
from special_release import MIGRATION_FAMILY, SpecialRelease, candidate_definition
from check_release_revision import passed, REQUIRED

GO, NODE = 'OnDemandGoImage', 'OnDemandNodeImage'


def release(order, plan=None):
    special = SpecialRelease(MagicMock(), '123456789012', 'a' * 40)
    special.previous = {GO: 'old-go', NODE: 'old-node'}
    special.plan = MagicMock(return_value=plan or {'schemaDigest': 'd', 'expectedSchema': 3})
    special.images = MagicMock(return_value={GO: 'new-go', NODE: 'new-node'})
    special.backup = MagicMock(side_effect=lambda: order.append('backup'))
    special.register_candidate = MagicMock(side_effect=lambda image: order.append('register ' + image) or 'candidate')
    special.run_migration = MagicMock(side_effect=lambda d, command: order.append(' '.join(command)))
    special.aws.call.side_effect = lambda *args, **kwargs: order.append('record') or {}
    special.change_images = MagicMock(side_effect=lambda images, suffix: order.append(suffix))
    special.verify = MagicMock(side_effect=lambda images: order.append('verify'))
    return special


class SpecialReleaseTest(unittest.TestCase):
    def test_schema_changes_before_the_new_code_serves_and_state_is_recorded_after_verification(self):
        order = []
        release(order).special_apply(digest='d', expected_schema=3, record_existing=False, compatible=True)
        self.assertEqual(order, ['backup', 'register new-go', 'migrate', 'verify-schema', 'record', 'update', 'verify'])

    def test_record_existing_never_runs_ddl(self):
        order = []
        release(order).special_apply(digest='d', expected_schema=3, record_existing=True, compatible=False)
        self.assertNotIn('migrate', order)
        self.assertIn('verify-schema', order)

    def test_unapproved_plan_or_unconfirmed_compatibility_stops_before_any_change(self):
        for kwargs in ({'digest': 'other', 'expected_schema': 3, 'record_existing': False, 'compatible': True},
                       {'digest': 'd', 'expected_schema': 2, 'record_existing': False, 'compatible': True},
                       {'digest': 'd', 'expected_schema': 3, 'record_existing': False, 'compatible': False}):
            order = []
            with self.assertRaises(ReleaseError):
                release(order).special_apply(**kwargs)
            self.assertEqual(order, [])

    def test_failed_acceptance_restores_old_images_but_not_the_schema(self):
        order = []
        special = release(order)
        special.verify = MagicMock(side_effect=[ReleaseError('HTTP failed'), None])
        with self.assertRaisesRegex(ReleaseError, 'schema was not rolled back'):
            special.special_apply(digest='d', expected_schema=3, record_existing=False, compatible=True)
        self.assertEqual(special.change_images.call_args_list[-1].args, ({GO: 'old-go', NODE: 'old-node'}, 'restore'))

    def test_candidate_definition_changes_only_the_image(self):
        current = {'family': MIGRATION_FAMILY, 'taskDefinitionArn': 'arn', 'revision': 4, 'status': 'ACTIVE',
                   'containerDefinitions': [{'name': 'go', 'image': 'old', 'secrets': [{'name': 'DATABASE_URL', 'valueFrom': 'ssm'}]}],
                   'taskRoleArn': 'task', 'executionRoleArn': 'exec', 'networkMode': 'awsvpc', 'cpu': '256', 'memory': '512'}
        candidate = candidate_definition(current, 'new')
        self.assertEqual(candidate['containerDefinitions'][0]['image'], 'new')
        self.assertEqual(candidate['containerDefinitions'][0]['secrets'], current['containerDefinitions'][0]['secrets'])
        self.assertEqual(current['containerDefinitions'][0]['image'], 'old')
        for key in ('taskDefinitionArn', 'revision', 'status'):
            self.assertNotIn(key, candidate)
        with self.assertRaises(ReleaseError):
            candidate_definition({**current, 'family': 'course-k-blog-production-on-demand-restore'}, 'new')

    def test_schema_digest_changes_on_sql_and_migration_engine_changes(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder); p = root / 'backend/internal/mysql'; (p / 'migrations').mkdir(parents=True)
            (p / 'migrations.go').write_text('engine'); (p / 'migrations/001_initial.sql').write_text('first')
            a = schema_digest(root); (p / 'migrations/001_initial.sql').write_text('second'); b = schema_digest(root)
            self.assertNotEqual(a, b); (p / 'migrations.go').write_text('new engine'); self.assertNotEqual(b, schema_digest(root))

    def test_exact_main_required_jobs_and_latest_attempt(self):
        jobs = [{'id': i, 'name': n, 'conclusion': 'success'} for i, n in enumerate(sorted(REQUIRED))]
        self.assertTrue(passed(jobs)); self.assertFalse(passed(jobs[:-1]))
        jobs.append({'id': 99, 'name': jobs[0]['name'], 'conclusion': 'failure'}); self.assertFalse(passed(jobs))


if __name__ == '__main__':
    unittest.main()
