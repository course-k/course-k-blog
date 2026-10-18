import io
import json
from pathlib import Path
import unittest
from probe import check

class Reply(io.BytesIO):
    status = 200
    def __init__(self, body, content_type='application/json'):
        super().__init__(body)
        self.headers = {'Content-Type': content_type}

class ProbeTest(unittest.TestCase):
    def test_deployed_probe_matches_tested_source(self):
        folder = Path(__file__).resolve().parent
        template = json.loads((folder.parent / 'aws/validation-stack.template.json').read_text())
        embedded = template['Resources']['ProbeFunction']['Properties']['Code']['ZipFile']
        self.assertEqual(embedded, (folder / 'probe.py').read_text())

    def test_checks_both_applications_and_html(self):
        paths = []
        class Opener:
            def open(self, url, timeout):
                paths.append(url)
                return Reply(b'<html></html>', 'text/html') if url.endswith('/') else Reply(b'{"ok":true}')
        check('https://example.test', Opener())
        self.assertEqual(paths, ['https://example.test/healthz','https://example.test/api/readyz','https://example.test/'])
    def test_failure_is_not_reported_as_success(self):
        for body in [b'{"ok":false}',b'<login>']:
            class Opener:
                def open(self, url, timeout): return Reply(body)
            with self.assertRaises((RuntimeError, ValueError)):
                check('https://example.test', Opener())

if __name__ == '__main__': unittest.main()
