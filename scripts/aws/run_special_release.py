#!/usr/bin/env python3
"""Translate workflow inputs into argv without interpolating them into a shell."""
import os
import subprocess
import sys
from pathlib import Path
args = [sys.executable, str(Path(__file__).with_name('special_release.py')), '--account', '757412973334',
        '--commit', os.environ['GITHUB_SHA'], '--mode', os.environ['RELEASE_MODE'],
        '--schema-digest', os.environ.get('RELEASE_DIGEST', ''), '--expected-schema', os.environ.get('RELEASE_SCHEMA', '0')]
if os.environ.get('RELEASE_COMPATIBLE') == 'true': args.append('--backward-compatible')
if os.environ.get('RELEASE_SYNC') == 'true': args.append('--check-sync')
raise SystemExit(subprocess.run(args).returncode)
