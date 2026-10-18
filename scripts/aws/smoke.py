#!/usr/bin/env python3
"""Explicit fake-article acceptance checks for a validation stack; changes that stack's data."""
import argparse
import getpass
import http.cookiejar
import json
import re
import sys
import urllib.error
import urllib.request
from urllib.parse import urlsplit
from core import Session, load_config
from validation import outputs

class NoRedirect(urllib.request.HTTPRedirectHandler):

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--cli')
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if not args.apply:
        parser.error('Fake-article writes require --apply.')
    session = Session(load_config(args.config), args.cli)
    origin = outputs(session.stack())['ApiEndpoint'].rstrip('/')
    url = urlsplit(origin)
    expected = '.execute-api.us-west-2.amazonaws.com'
    if url.scheme != 'https' or not url.hostname or (not url.hostname.endswith(expected)) or url.path or url.query or url.fragment or url.port or url.username:
        raise ValueError('Only the generated Oregon API endpoint of this validation stack is supported.')
    password = getpass.getpass('Validation administrator password (not saved): ')
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), NoRedirect())
    checks = []

    def req(path, method='GET', body=None, headers=None):
        h = {'Origin': origin}
        h.update(headers or {})
        if body is not None:
            h['Content-Type'] = 'application/json'
        r = urllib.request.Request(origin + path, data=None if body is None else json.dumps(body).encode(), headers=h, method=method)
        try:
            with client.open(r, timeout=35) as response:
                return (response.status, response.headers, response.read())
        except urllib.error.HTTPError as response:
            return (response.code, response.headers, response.read())

    def check(name, condition):
        if not condition:
            raise ValueError('Check failed: ' + name)
        checks.append(name)
        print('PASS', name, flush=True)

    def api(path, method='GET', body=None, status=200):
        s, h, b = req(path, method, body)
        check(method + ' ' + path + ' status ' + str(status), s == status)
        return json.loads(b)
    check('readiness', api('/api/readyz')['ok'])
    api('/api/admin/posts', status=401)
    s, h, b = req('/api/session', 'POST', {'password': password}, headers={'Origin': 'https://foreign.invalid'})
    check('foreign origin rejected', s == 403)
    s, h, b = req('/api/session', 'POST', {'password': password})
    check('login', s == 200)
    cookies = h.get_all('Set-Cookie') or []
    check('secure session cookie', any(('Secure' in c and 'HttpOnly' in c and ('SameSite=Strict' in c) for c in cookies)))
    post = api('/api/admin/posts', 'POST', {'title': 'AWS validation draft', 'markdown': '## Validation heading\n\nvalidation-private-draft'}, status=201)
    id = post['id']
    path = '/api/admin/posts/' + id
    api('/api/posts/' + id, status=404)
    published = api(path + '/publish', 'POST', {'version': post['version']})
    stamp = published['publishedAt']
    public = api('/api/posts/' + id)
    check('published body visible', public['markdown'] == post['markdown'])
    s, h, b = req('/posts/' + id)
    text = b.decode()
    check('SSR title and body without JS', s == 200 and 'AWS validation draft' in text and ('validation-private-draft' in text))
    check('adopted UI and TOC deployed', 'course_kのブログ' in text and 'article-section-1' in text)
    check('HTML security headers', h.get('X-Content-Type-Options') == 'nosniff' and h.get('X-Frame-Options') == 'DENY')
    assets = re.findall('(?:src|href)="([^" ]+\\.(?:js|css)(?:\\?[^" ]*)?)"', text)
    local = [a for a in assets if a.startswith('/')]
    check('static asset references present', bool(local))
    for a in local[:3]:
        st, hd, bd = req(a)
        check('static asset MIME ' + a, st == 200 and bool(bd) and ('javascript' in hd.get('Content-Type', '') or 'css' in hd.get('Content-Type', '')))
    saved = api(path, 'PUT', {'version': published['version'], 'title': 'Unpublished changed title', 'markdown': 'validation-working-copy-only'})
    check('old published body retained after draft save', api('/api/posts/' + id)['markdown'] == post['markdown'])
    api(path, 'PUT', {'version': published['version'], 'title': 'stale', 'markdown': 'stale'}, status=409)
    updated = api(path + '/publish', 'POST', {'version': saved['version']})
    check('initial publication date retained', updated['publishedAt'] == stamp and updated['id'] == id)
    check('updated published body', api('/api/posts/' + id)['markdown'] == 'validation-working-copy-only')
    hidden = api(path + '/unpublish', 'POST', {'version': updated['version']})
    api('/api/posts/' + id, status=404)
    s, h, b = req('/posts/' + id)
    check('unpublished SSR gives 404', s == 404)
    api('/api/session', 'DELETE', {})
    api('/api/admin/posts', status=401)
    print('Completed', len(checks), 'checks. Fake article retained as an unpublished draft; no credentials recorded.')
if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, RuntimeError):
        print('Validation smoke failed; inspect the validation stack. Raw responses suppressed.', file=sys.stderr)
        sys.exit(1)
