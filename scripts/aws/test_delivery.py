import copy
import unittest
from unittest.mock import MagicMock, patch

from delivery import Release, ReleaseError, image_reference, review_changes, wait_for, validate_service


class DeliveryTest(unittest.TestCase):
    def test_images_must_be_pinned_to_expected_repository(self):
        repo = '123456789012.dkr.ecr.us-west-2.amazonaws.com/course-k-blog-production-noderepository-x'
        self.assertEqual(image_reference(repo, 'sha256:' + 'a' * 64), repo + '@sha256:' + 'a' * 64)
        for uri, digest in [(repo, 'latest'), (repo.replace('us-west-2', 'us-east-1'), 'sha256:' + 'a'*64),
                            ('attacker.invalid/repo', 'sha256:' + 'a'*64)]:
            with self.assertRaises(ReleaseError):
                image_reference(uri, digest)

    def test_change_set_rejects_storage_iam_and_unexpected_properties(self):
        def change(name, **extra):
            return {'ResourceChange': {'LogicalResourceId': name, 'Action': 'Modify',
                                       'Replacement': 'True' if name == 'ApplicationTask' else 'False', **extra}}
        app = change('ApplicationTask')
        review_changes([app, change('ApplicationService')])
        for extra in [change('Database'), change('ImagesBucket'), change('ApplicationTaskRole'),
                      change('ApplicationService', Replacement='True'),
                      change('ApplicationTask', Action='Remove'),
                      change('ApplicationCPUUtilizationAlarm', Details=[{'Target': {'Name': 'AlarmActions'}}])]:
            with self.assertRaises(ReleaseError):
                review_changes([app, extra])
        with self.assertRaises(ReleaseError):
            review_changes([])

    def test_service_must_be_stable_with_current_stack_definition(self):
        service = {'desiredCount': 1, 'runningCount': 1, 'pendingCount': 0,
                   'taskDefinition': 'expected', 'deployments': [{'rolloutState': 'COMPLETED'}]}
        validate_service(service, 'expected')
        for update in [{'runningCount': 0}, {'pendingCount': 1}, {'taskDefinition': 'other'},
                       {'deployments': [{'rolloutState': 'IN_PROGRESS'}]}]:
            with self.assertRaises(ReleaseError):
                validate_service({**copy.deepcopy(service), **update}, 'expected')

    def test_wait_is_bounded_and_terminal_failures_do_not_pass(self):
        with patch('delivery.time.sleep'), patch('delivery.time.monotonic', side_effect=[0, 1, 3]):
            with self.assertRaisesRegex(ReleaseError, 'Timed out'):
                wait_for(lambda: None, timeout=2, interval=0)
        with self.assertRaisesRegex(ReleaseError, 'failed'):
            wait_for(lambda: (_ for _ in ()).throw(ReleaseError('failed')))
        self.assertEqual(wait_for(lambda: {'complete': True}), {'complete': True})

    def test_wrong_identity_stops_before_any_stack_or_mutation(self):
        aws = MagicMock()
        aws.call.return_value = {'Account': '999999999999', 'Arn': 'not-the-deployer'}
        with self.assertRaisesRegex(ReleaseError, 'identity'):
            Release(aws, '123456789012', 'a' * 40).preflight()
        aws.stack.assert_not_called()
        self.assertEqual(aws.call.call_count, 1)

    def test_backup_failure_prevents_production_change(self):
        release = Release(MagicMock(), '123456789012', 'a' * 40)
        release.preflight = MagicMock()
        release.images = MagicMock(return_value={'NodeImage': 'candidate'})
        release.backup = MagicMock(side_effect=ReleaseError('backup failed'))
        release.change_images = MagicMock()
        with self.assertRaisesRegex(ReleaseError, 'backup failed'):
            release.apply()
        release.change_images.assert_not_called()

    def test_failed_public_verification_restores_previous_images_and_fails_release(self):
        release = Release(MagicMock(), '123456789012', 'a' * 40)
        release.previous = {k: 'old-' + k for k in ('NodeImage', 'GoImage', 'RouterImage')}
        release.old_task = 'old-task'
        release.preflight = MagicMock()
        candidate = {k: 'new-' + k for k in release.previous}
        release.images = MagicMock(return_value=candidate)
        release.backup = MagicMock()
        release.service = MagicMock(return_value={
            'desiredCount': 1, 'runningCount': 1, 'taskDefinition': 'old-task',
            'deployments': [{'rolloutState': 'COMPLETED'}]})
        release.change_images = MagicMock()
        release.verify = MagicMock(side_effect=[ReleaseError('HTTP failed'), None])
        with self.assertRaisesRegex(ReleaseError, 'previous images restored'):
            release.apply()
        self.assertEqual(release.change_images.call_args_list[-1].args, (release.previous, 'restore'))
        release.verify.assert_called_with(release.previous)


if __name__ == '__main__':
    unittest.main()
