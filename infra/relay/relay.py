"""Fetch relay for media without an IPv6 address.

The API runs in a network whose only way out is IPv6. This function runs
outside it and fetches a fixed set of public pages on the API's behalf. It
holds no credentials and has no access to the database or storage.
"""
import base64
import urllib.error
import urllib.request

# Exactly the pages the external sync reads. Anything else is refused.
ALLOWED = frozenset({
    'https://note.com/course__k/rss',
    'https://www.wantedly.com/robots.txt',
    'https://www.wantedly.com/id/course_k/story-list',
})
# The answer is returned in one invocation result, which is limited in size.
MAX_BYTES = 4 * 1024 * 1024

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def fetch(event, opener):
    url = event.get('url') if isinstance(event, dict) else None
    if not isinstance(url, str) or url not in ALLOWED:
        return {'error': 'url_not_allowed'}
    request = urllib.request.Request(url, headers={'User-Agent': 'CourseKBlog/1.0'}, method='GET')
    try:
        with opener.open(request, timeout=15) as response:
            body = response.read(MAX_BYTES + 1)
            status = response.status
    except urllib.error.HTTPError as error:
        # A refusal or redirect from the medium is an answer, not a relay failure.
        return {'status': error.code, 'body': ''}
    except Exception:
        return {'error': 'upstream_unavailable'}
    if len(body) > MAX_BYTES:
        return {'error': 'response_too_large'}
    return {'status': status, 'body': base64.b64encode(body).decode()}

def handler(event, context):
    return fetch(event, urllib.request.build_opener(NoRedirect))
