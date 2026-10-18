#!/usr/bin/env python3
"""Validate tracked/new repository documents and JSON without contacting services."""
import ast
import json
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]


def main():
    names = subprocess.check_output(
        ['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT,
    ).decode().strip('\0').split('\0')
    paths = [ROOT / name for name in set(names) if name and (ROOT / name).is_file()]
    errors = []
    links = 0
    for path in sorted(paths):
        relative = path.relative_to(ROOT)
        try:
            if path.suffix == '.json':
                json.loads(path.read_text())
            if path.suffix == '.py':
                ast.parse(path.read_text(), filename=str(relative))
            if path.suffix != '.md':
                continue
            # Examples inside fenced code are not navigation links.
            text = re.sub(r'(?ms)^```.*?^```[^\n]*', '', path.read_text())
            for target in re.findall(r'!?\[[^\]]*\]\(([^\s)]+)\)', text):
                if re.match(r'^[a-zA-Z][a-zA-Z0-9+.-]*:|^/|^#', target):
                    continue
                links += 1
                location = unquote(target.split('#', 1)[0])
                if not (path.parent / location).exists():
                    errors.append(f'{relative}: missing local link {target}')
        except (ValueError, SyntaxError, UnicodeError):
            # File names are useful; content and parameter values need not be printed.
            errors.append(f'{relative}: invalid syntax or encoding')
    whitespace = subprocess.run(['git', 'diff', '--check', 'HEAD'], cwd=ROOT)
    if errors or whitespace.returncode:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    print(f'Repository checks passed: {links} local links; JSON and Python syntax. External URLs and Markdown anchors are not checked.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
