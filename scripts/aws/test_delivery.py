import unittest
from unittest.mock import MagicMock, patch

from delivery import Release, ReleaseError, image_reference, review_changes, wait_for

GO, NODE = 'OnDemandGoImage', 'OnDemandNodeImage'


def change(name, replacement='False', details=None, action='Modify'):
    if details is None:
        if name in ('OnDemandApiFunction', 'OnDemandWebFunction'):
            details = [{'Evaluation': 'Static', 'ChangeSource': 'ParameterReference', 'CausingEntity': GO, 'Target': {'Name': 'Code'}},
                       {'Evaluation': 'Dynamic', 'ChangeSource': 'DirectModification', 'Target': {'Name': 'Code'}}]
        elif name in ('OnDemandMigrationTask', 'OnDemandRestoreTask'):
            details = [{'Evaluation': 'Static', 'ChangeSource': 'ParameterReference', 'CausingEntity': GO,
                        'Target': {'Name': 'ContainerDefinitions'}}]
        else:
            details = [{'Evaluation': 'Dynamic', 'ChangeSource': 'ResourceAttribute', 'CausingEntity': 'OnDemandApiFunction.Arn',
                        'Target': {'Name': 'TargetFunctionArn'}}]
    return {'ResourceChange': {'LogicalResourceId': name, 'Action': action, 'Replacement': replacement, 'Details': details}}


# What CloudFormation listed for an image-only update of the applied stack.
OBSERVED = [change('OnDemandApiFunction'), change('OnDemandWebFunction'), change('OnDemandApiUrl', 'Conditional'),
            change('OnDemandWebUrl', 'Conditional'), change('OnDemandDailyWork'), change('OnDemandDailyWorkPermission', 'Conditional'),
            change('OnDemandDistribution'), change('OnDemandProbeFunction'), change('OnDemandProbeSchedule'),
            change('OnDemandProbePermission', 'Conditional'), change('OnDemandMigrationTask', 'True'),
            change('OnDemandRestoreTask', 'True')]


def function(image, state='Active', status='Successful'):
    return {'Configuration': {'State': state, 'LastUpdateStatus': status}, 'Code': {'ImageUri': image}}


class DeliveryTest(unittest.TestCase):
    def test_images_must_be_pinned_to_expected_repository(self):
        repo = '123456789012.dkr.ecr.us-west-2.amazonaws.com/course-k-blog-production-noderepository-x'
        self.assertEqual(image_reference(repo, 'sha256:' + 'a' * 64), repo + '@sha256:' + 'a' * 64)
        for uri, digest in [(repo, 'latest'), (repo.replace('us-west-2', 'us-east-1'), 'sha256:' + 'a'*64),
                            ('attacker.invalid/repo', 'sha256:' + 'a'*64)]:
            with self.assertRaises(ReleaseError):
                image_reference(uri, digest)

    def test_the_observed_image_update_is_allowed(self):
        review_changes(OBSERVED)

    def test_change_set_rejects_anything_beyond_the_images(self):
        api = change('OnDemandApiFunction')
        for extra in [change('OnDemandCluster'), change('ImagesBucket'), change('OnDemandApiRole'),
                      change('OnDemandApiFunction', 'True'), change('OnDemandApiFunction', action='Remove'),
                      change('OnDemandApiFunction', details=[{'Evaluation': 'Static', 'ChangeSource': 'DirectModification',
                                                              'Target': {'Name': 'Environment'}}]),
                      change('OnDemandDistribution', details=[{'Evaluation': 'Static', 'ChangeSource': 'DirectModification',
                                                               'Target': {'Name': 'DistributionConfig'}}]),
                      change('OnDemandApiUrl', 'True'), change('OnDemandApiUrl', details=[])]:
            with self.assertRaises(ReleaseError):
                review_changes([api, extra])
        with self.assertRaises(ReleaseError):
            review_changes([])
        with self.assertRaisesRegex(ReleaseError, 'No application image'):
            review_changes([change('OnDemandMigrationTask', 'True')])

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

    def test_functions_must_run_the_stack_images_and_be_settled(self):
        aws = MagicMock()
        release = Release(aws, '123456789012', 'a' * 40)
        release.functions = {GO: 'api', NODE: 'web'}
        images = {GO: 'go@1', NODE: 'node@1'}
        aws.call.side_effect = [function('go@1'), function('node@1')]
        release.check_functions(images)
        self.assertEqual([c.kwargs['data'] for c in aws.call.call_args_list],
                         [{'FunctionName': 'api'}, {'FunctionName': 'web'}])
        for response in (function('go@0'), function('go@1', status='InProgress'), function('go@1', state='Pending')):
            aws.call.side_effect = [response, function('node@1')]
            with self.assertRaises(ReleaseError):
                release.check_functions(images)

    def test_pending_work_passes_invoke_arguments_as_options(self):
        aws = MagicMock()
        release = Release(aws, '123456789012', 'a' * 40)
        release.functions = {GO: 'api', NODE: 'web'}

        def invoke(service, action, *, extra=(), **kwargs):
            self.assertNotIn('data', kwargs)
            self.assertEqual(extra[:4], ['--function-name', 'api', '--payload', '{"work":"pending"}'])
            with open(extra[-1], 'w') as stream:
                stream.write('{"statusCode":200}')
            return {'StatusCode': 200}

        aws.call.side_effect = invoke
        release.backup()

        def failed(service, action, *, extra=(), **kwargs):
            with open(extra[-1], 'w') as stream:
                stream.write('{"statusCode":500}')
            return {'StatusCode': 200}

        aws.call.side_effect = failed
        with self.assertRaisesRegex(ReleaseError, 'Pending work failed'):
            release.backup()

    def test_pending_work_failure_prevents_production_change(self):
        release = Release(MagicMock(), '123456789012', 'a' * 40)
        release.preflight = MagicMock()
        release.images = MagicMock(return_value={NODE: 'candidate'})
        release.backup = MagicMock(side_effect=ReleaseError('Pending work failed'))
        release.change_images = MagicMock()
        with self.assertRaisesRegex(ReleaseError, 'Pending work failed'):
            release.apply()
        release.change_images.assert_not_called()

    def test_retry_after_completed_rollback_but_not_during_cleanup(self):
        aws = MagicMock()
        images = {NODE: 'candidate'}
        aws.stack.side_effect = [
            {'StackStatus': 'UPDATE_ROLLBACK_COMPLETE', 'Parameters': [{'ParameterKey': NODE, 'ParameterValue': 'old'}]},
            {'StackStatus': 'UPDATE_COMPLETE', 'Parameters': [{'ParameterKey': NODE, 'ParameterValue': 'candidate'}]},
        ]
        aws.call.side_effect = [{'Id': 'release-change-set'}, {'Status': 'CREATE_COMPLETE', 'Changes': OBSERVED}, {}]
        release = Release(aws, '123456789012', 'a' * 40)
        release.change_images(images, 'update')
        self.assertEqual(aws.call.call_args_list[-1].args[:2], ('cloudformation', 'execute-change-set'))
        aws.reset_mock()
        aws.stack.side_effect = None
        aws.stack.return_value = {'StackStatus': 'UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS'}
        with self.assertRaisesRegex(ReleaseError, 'Stack changed'):
            release.change_images(images, 'update')
        aws.call.assert_not_called()

    def test_failed_verification_restores_previous_images_and_fails_release(self):
        release = Release(MagicMock(), '123456789012', 'a' * 40)
        release.previous = {GO: 'old-go', NODE: 'old-node', 'PublicOrigin': 'https://blog.course-k.com'}
        release.preflight = MagicMock()
        candidate = {GO: 'new-go', NODE: 'new-node'}
        release.images = MagicMock(return_value=candidate)
        release.backup = MagicMock()
        release.change_images = MagicMock()
        release.verify = MagicMock(side_effect=[ReleaseError('HTTP failed'), None])
        with self.assertRaisesRegex(ReleaseError, 'previous images restored'):
            release.apply()
        previous = {GO: 'old-go', NODE: 'old-node'}
        self.assertEqual(release.change_images.call_args_list[-1].args, (previous, 'restore'))
        release.verify.assert_called_with(previous)

    def test_verification_waits_for_functions_then_purges_before_checking_pages(self):
        release = Release(MagicMock(), '123456789012', 'a' * 40)
        order = []
        release.check_functions = MagicMock(side_effect=[ReleaseError('updating'), None])
        release.purge = MagicMock(side_effect=lambda: order.append('purge'))
        response = MagicMock(status=200)
        response.__enter__.return_value = response
        response.geturl.side_effect = lambda: order[-1]
        response.read.return_value = b'course_k'

        def opened(url, timeout):
            order.append(url)
            return response

        with patch('delivery.time.sleep'), patch('delivery.urllib.request.urlopen', side_effect=opened):
            release.verify({GO: 'go@1', NODE: 'node@1'})
        self.assertEqual(release.check_functions.call_count, 2)
        self.assertEqual(order[0], 'purge')
        self.assertEqual(order[1:], ['https://blog.course-k.com/healthz', 'https://blog.course-k.com/api/readyz',
                                     'https://blog.course-k.com/'])

    def test_stack_failure_logs_only_denied_action_names(self):
        aws = MagicMock()
        aws.stack.side_effect = [
            {'StackStatus': 'UPDATE_COMPLETE', 'Parameters': []},
            {'StackStatus': 'UPDATE_ROLLBACK_IN_PROGRESS'},
        ]
        aws.call.side_effect = [
            {'Id': 'change-set'},
            {'Status': 'CREATE_COMPLETE', 'Changes': OBSERVED},
            {},
            {'StackEvents': [{'ResourceStatusReason':
                             'private-secret-value not authorized to perform: lambda:UpdateFunctionCode on resource: private-arn'}]},
        ]
        with self.assertRaises(ReleaseError) as caught:
            Release(aws, '123456789012', 'a' * 40).change_images({}, 'update')
        self.assertIn('denied actions: lambda:UpdateFunctionCode', str(caught.exception))
        self.assertNotIn('private', str(caught.exception))


if __name__ == '__main__':
    unittest.main()
