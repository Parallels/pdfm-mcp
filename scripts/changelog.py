#!/usr/bin/env python3
"""Generate reviewed release PR changelogs, or extract a release's notes."""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sys

from version import parse_version


def section(text, version):
    matches = list(re.finditer(r"^## \[([^\]\n]+)\][^\n]*$", text, re.MULTILINE))
    found = [i for i, match in enumerate(matches) if match[1] == version]
    if len(found) != 1:
        raise ValueError(f"expected exactly one changelog section for {version}")
    index = found[0]
    start = matches[index].end()
    end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
    body = re.sub(r"<!--[\s\S]*?-->", "", text[start:end]).strip()
    if not body or not any(line.strip() not in ("-", "*", "### Changes") for line in body.splitlines()):
        raise ValueError(f"changelog section for {version} is empty")
    return body


def entries_from_prs(prs):
    entries = []
    for pr in sorted(prs, key=lambda p: (p["mergedAt"], p["number"])):
        if (re.fullmatch(r"release/[0-9]+\.[0-9]+\.[0-9]+", pr.get("headRefName", ""))
                or any(label["name"] == "release-request" for label in pr.get("labels", []))):
            continue
        in_section = False
        bullets = []
        body = re.sub(r"<!--[\s\S]*?-->", "", pr.get("body") or "")
        for line in body.splitlines():
            if line.startswith("## "):
                in_section = line.strip() == "## Changelog"
            elif in_section and re.match(r"^\s*-\s+\S", line):
                bullets.append(line.strip())
            elif in_section and bullets and re.match(r"^\s+\S", line):
                bullets[-1] += "\n  " + line.strip()
        if not bullets:
            title = " ".join(pr["title"].split())
            bullets = [f"- {title} (#{pr['number']})"]
        entries.extend(bullets)
    return list(dict.fromkeys(entries)) or ["- chore: maintenance release."]


def generate(text, version, prs):
    if re.search(r"^## \[" + re.escape(version) + r"\]", text, re.MULTILINE):
        raise ValueError(f"changelog already contains {version}")
    date = datetime.now(timezone.utc).date().isoformat()
    block = f"## [{version}] - {date}\n\n" + "\n".join(entries_from_prs(prs)) + "\n\n"
    first = re.search(r"^## \[", text, re.MULTILINE)
    index = first.start() if first else len(text)
    return text[:index].rstrip() + "\n\n" + block + text[index:]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("generate", "notes"))
    parser.add_argument("--version", required=True)
    parser.add_argument("--changelog", default="CHANGELOG.md")
    parser.add_argument("--prs", help="JSON from gh pr list (generate only)")
    parser.add_argument("--output", help="Destination for release notes (notes only)")
    args = parser.parse_args()
    try:
        parse_version(args.version, stable=True)
        path = Path(args.changelog)
        text = path.read_text()
        if args.command == "generate":
            if not args.prs:
                raise ValueError("generate requires --prs")
            prs = json.loads(Path(args.prs).read_text())
            if not isinstance(prs, list) or len(prs) >= 1000:
                raise ValueError("PR result may be truncated; split the release interval")
            path.write_text(generate(text, args.version, prs))
        else:
            if not args.output:
                raise ValueError("notes requires --output")
            Path(args.output).write_text(section(text, args.version) + "\n")
        print(f"changelog: {args.command} {args.version}", file=sys.stderr)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(f"changelog: error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
