#!/usr/bin/env python3
"""Version management for spec 023. Machine output goes to files, never stdout."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

CORE = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
SEMVER = re.compile(CORE + r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?")


def parse_version(value, stable=False):
    match = SEMVER.fullmatch(value) if len(value) <= 64 else None
    if not match or (stable and match[4]):
        raise ValueError("expected MAJOR.MINOR.PATCH" if stable else "invalid SemVer (no v/build metadata)")
    prerelease = match[4]
    if prerelease and any(p.isdigit() and len(p) > 1 and p[0] == "0" for p in prerelease.split(".")):
        raise ValueError("numeric prerelease identifiers cannot have leading zeros")
    return tuple(int(match[i]) for i in (1, 2, 3)), prerelease


def read_version(path):
    value = Path(path).read_text().strip()
    parse_version(value, stable=True)
    return value


def bump_version(value, kind):
    (major, minor, patch), _ = parse_version(value, stable=True)
    if kind == "major":
        return f"{major + 1}.0.0"
    if kind == "minor":
        return f"{major}.{minor + 1}.0"
    if kind == "patch":
        return f"{major}.{minor}.{patch + 1}"
    raise ValueError("invalid bump type")


PLUGIN_MANIFEST = Path("plugins/pdfm-mcp/.codex-plugin/plugin.json")


def sync_plugin_manifest(version_file, version):
    """Set the Codex plugin version next to VERSION; skip when the manifest is absent."""
    manifest = Path(version_file).resolve().parent / PLUGIN_MANIFEST
    if not manifest.is_file():
        return
    data = json.loads(manifest.read_text())
    data["version"] = version
    manifest.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n")


def resolve_version(base, environment=None, run_number=None, tag=None):
    parse_version(base, stable=True)
    if tag is not None:
        if not tag.startswith("v"):
            raise ValueError("tag must start with v")
        version = tag[1:]
        _, prerelease = parse_version(version)
        if version.split("-", 1)[0] != base:
            raise ValueError("tag base does not match VERSION")
        channel = "prerelease" if prerelease else "stable"
        if prerelease and prerelease.split(".")[0] in ("canary", "beta"):
            channel = prerelease.split(".")[0]
    elif environment in ("canary", "beta"):
        if not run_number or not re.fullmatch(r"[1-9][0-9]{0,19}", run_number):
            raise ValueError("canary/beta require a positive run number without leading zeros")
        channel = environment
        version = f"{base}-{channel}.{run_number}"
        parse_version(version)
    elif environment == "production":
        version, channel = base, "stable"
    else:
        raise ValueError("environment must be canary, beta or production")
    return {
        "base_version": base,
        "release_version": version,
        "release_tag": f"v{version}",
        "release_channel": channel,
        "prerelease": str(channel != "stable").lower(),
    }


def write_outputs(values, path):
    if path:
        with Path(path).open("a") as output:
            for key, value in values.items():
                output.write(f"{key}={value}\n")


def git(*args, optional=False):
    result = subprocess.run(["git", *args], text=True, capture_output=True, check=False)
    if result.returncode and not optional:
        raise ValueError(f"git {args[0]} failed")
    return result


def guard_release(tag, base):
    values = resolve_version(base, tag=tag)
    if git("status", "--porcelain").stdout:
        raise ValueError("working tree is dirty")
    head = git("rev-parse", "--verify", "HEAD^{commit}").stdout.strip()
    tagged = git("rev-parse", "-q", "--verify", f"refs/tags/{tag}^{{commit}}", optional=True)
    if tagged.returncode == 0 and tagged.stdout.strip() != head:
        raise ValueError("HEAD differs from the tagged commit")
    # origin/main is authoritative when available; local fixtures use main.
    remote = git("rev-parse", "-q", "--verify", "refs/remotes/origin/main", optional=True)
    main = "refs/remotes/origin/main" if remote.returncode == 0 else "refs/heads/main"
    if git("merge-base", "--is-ancestor", head, main, optional=True).returncode:
        raise ValueError("release commit is not reachable from main")
    target, _ = parse_version(values["release_version"])
    for existing in git("tag", "--list", "v*").stdout.splitlines():
        if existing == tag:
            continue
        try:
            core, prerelease = parse_version(existing[1:])
        except ValueError:
            continue
        if not prerelease and core >= target:
            raise ValueError(f"VERSION must be newer than existing stable tag {existing}")
    return values


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("current", "bump", "resolve", "guard"))
    parser.add_argument("--file", default="VERSION")
    parser.add_argument("--type", choices=("major", "minor", "patch"), default="patch")
    parser.add_argument("--environment", choices=("canary", "beta", "production"))
    parser.add_argument("--run-number")
    parser.add_argument("--tag")
    parser.add_argument("--output", default=os.environ.get("GITHUB_OUTPUT"))
    args = parser.parse_args()
    try:
        base = read_version(args.file)
        if args.command == "current":
            values = {"version": base}
        elif args.command == "bump":
            version = bump_version(base, args.type)
            parse_version(version, stable=True)
            Path(args.file).write_text(version + "\n")
            sync_plugin_manifest(args.file, version)
            values = {"version": version}
        elif args.command == "resolve":
            if args.tag is not None and args.environment is not None:
                raise ValueError("select either --tag or --environment")
            values = resolve_version(base, args.environment, args.run_number, args.tag)
        else:
            if not args.tag:
                raise ValueError("guard requires --tag")
            values = guard_release(args.tag, base)
        write_outputs(values, args.output)
        print("version: " + ", ".join(f"{k}={v}" for k, v in values.items()), file=sys.stderr)
    except (ValueError, OSError) as error:
        print(f"version: error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
