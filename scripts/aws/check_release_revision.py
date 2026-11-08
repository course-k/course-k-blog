#!/usr/bin/env python3
"""Require current main and successful prerequisite jobs for its exact commit."""
import argparse
import json
import os
import re
import subprocess
import urllib.request

REQUIRED = {'Repository and infrastructure', 'Frontend and offline UI', 'Go and real MySQL', 'Browser, SSR, Go and MySQL'}

def passed(jobs):
    # Select the latest attempt for each required job, not an older success.
    latest = {}
    for job in sorted(jobs, key=lambda x: x['id']):
        if job['name'] in REQUIRED: latest[job['name']] = job.get('conclusion')
    return set(latest) == REQUIRED and all(value == 'success' for value in latest.values())

if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--commit', required=True); args = parser.parse_args()
    if not re.fullmatch('[a-f0-9]{40}', args.commit): raise SystemExit('Invalid commit')
    main = subprocess.check_output(['git', 'ls-remote', 'https://github.com/course-k/course-k-blog.git', 'refs/heads/main'], text=True).split()[0]
    if main != args.commit: raise SystemExit('Refusing a superseded main revision')
    token = os.environ['GH_TOKEN']
    def get(path):
        req = urllib.request.Request('https://api.github.com/repos/course-k/course-k-blog/' + path,
                                     headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json'})
        with urllib.request.urlopen(req, timeout=30) as response: return json.load(response)
    runs = get('actions/workflows/checks.yml/runs?head_sha=' + args.commit + '&event=push&per_page=100')['workflow_runs']
    if not runs: raise SystemExit('No main verification run exists for this commit')
    run = max(runs, key=lambda x: x['id'])
    jobs = get(f'actions/runs/{run["id"]}/jobs?filter=latest&per_page=100')['jobs']
    if not passed(jobs): raise SystemExit('Required main verification jobs have not succeeded')
    print('Current main and all four verification jobs confirmed')
