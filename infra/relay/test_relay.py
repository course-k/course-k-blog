import base64
import io
import unittest
import urllib.error
from relay import ALLOWED, fetch

class Reply(io.BytesIO):
    def __init__(self, body, status=200):
        super().__init__(body)
        self.status = status

class Opener:
    """Records what was requested and answers from a fixed reply or error."""
    def __init__(self, reply=None, error=None):
        self.reply, self.error, self.requests = reply, error, []
    def open(self, request, timeout):
        self.requests.append((request.full_url, request.get_method(), dict(request.header_items()), timeout))
        if self.error:
            raise self.error
        return self.reply

class RelayTest(unittest.TestCase):
    def test_fetches_an_allowed_page_and_returns_its_bytes(self):
        opener = Opener(Reply('記事'.encode()))
        result = fetch({'url': 'https://note.com/course__k/rss'}, opener)
        self.assertEqual(result, {'status': 200, 'body': base64.b64encode('記事'.encode()).decode()})
        url, method, headers, timeout = opener.requests[0]
        self.assertEqual((url, method), ('https://note.com/course__k/rss', 'GET'))
        self.assertEqual(headers, {'User-agent': 'CourseKBlog/1.0'})
        self.assertLessEqual(timeout, 15)

    def test_every_page_the_sync_reads_is_allowed(self):
        self.assertEqual(ALLOWED, frozenset({
            'https://note.com/course__k/rss',
            'https://www.wantedly.com/robots.txt',
            'https://www.wantedly.com/id/course_k/story-list',
        }))

    def test_refuses_anything_else_without_fetching(self):
        refused = [
            'https://note.com/someone_else/rss',
            'https://note.com/course__k/rss?x=1',
            'http://note.com/course__k/rss',
            'https://169.254.169.254/latest/meta-data/',
            'https://note.com.evil.example/course__k/rss',
            'file:///etc/passwd',
            '', None, 7,
        ]
        for url in refused:
            opener = Opener(Reply(b'secret'))
            self.assertEqual(fetch({'url': url}, opener), {'error': 'url_not_allowed'}, url)
            self.assertEqual(opener.requests, [], url)
        self.assertEqual(fetch({}, Opener()), {'error': 'url_not_allowed'})
        self.assertEqual(fetch('not an object', Opener()), {'error': 'url_not_allowed'})

    def test_upstream_status_is_reported_not_hidden(self):
        error = urllib.error.HTTPError('https://note.com/course__k/rss', 503, 'busy', {}, io.BytesIO(b'detail'))
        self.assertEqual(fetch({'url': 'https://note.com/course__k/rss'}, Opener(error=error)), {'status': 503, 'body': ''})

    def test_unreachable_upstream_is_an_error_without_details(self):
        result = fetch({'url': 'https://note.com/course__k/rss'}, Opener(error=OSError('sensitive-upstream-detail')))
        self.assertEqual(result, {'error': 'upstream_unavailable'})

    def test_oversized_page_is_refused_rather_than_truncated(self):
        result = fetch({'url': 'https://note.com/course__k/rss'}, Opener(Reply(b'x' * (4 * 1024 * 1024 + 1))))
        self.assertEqual(result, {'error': 'response_too_large'})

if __name__ == '__main__': unittest.main()
