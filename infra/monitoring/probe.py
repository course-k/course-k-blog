"""External read-only probe. No credentials, response bodies or request headers logged."""
import json
import os
import urllib.request

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def probe_paths(env):
    # A stack whose database pauses when idle leaves out the database check.
    if 'PROBE_PATHS' not in env:
        return ['/healthz', '/api/readyz', '/']
    paths = [path.strip() for path in env['PROBE_PATHS'].split(',') if path.strip()]
    if not paths:
        raise ValueError('no probe paths')
    return paths

def check(base, opener, paths=('/healthz', '/api/readyz', '/')):
    # The first page is '/'; '/?page=1' redirects there and redirects are not followed.
    for path in paths:
        with opener.open(base.rstrip('/') + path, timeout=5) as response:
            if response.status != 200:
                raise RuntimeError('probe status failure')
            if path != '/':
                if json.loads(response.read(1024)).get('ok') is not True:
                    raise RuntimeError('probe body failure')
            elif 'text/html' not in response.headers.get('Content-Type', ''):
                raise RuntimeError('probe content type failure')

def handler(event, context):
    # The endpoint is configured by the stack, never supplied by a public event.
    try:
        check(os.environ['ENDPOINT'], urllib.request.build_opener(NoRedirect), probe_paths(os.environ))
    except Exception:
        print('external probe failed')
        raise RuntimeError('external probe failed') from None
    return {'ok': True}
