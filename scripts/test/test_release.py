"""Behavioral tests for version resolution, file output and reviewed notes."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
import changelog
import version


class VersionTests(unittest.TestCase):
    def test_bump_resets_lower_components(self):
        for kind, expected in [("major", "2.0.0"), ("minor", "1.3.0"), ("patch", "1.2.10")]:
            with self.subTest(kind=kind):
                self.assertEqual(version.bump_version("1.2.9", kind), expected)

    def test_invalid_semver(self):
        for value in ["", "1.2", "01.2.3", "1.02.3", "1.2.03", "v1.2.3", "1.2.3+build",
                      "1.2.3-", "1.2.3-rc..1", "1.2.3-rc.01", "1.2.3-00", "1.2.3-rc.",
                      "1.2.3;id", "1.2.3-$(id)", "1.2.3\nx=y", "1.2.3-" + "a" * 65]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                version.parse_version(value)

    def test_valid_prerelease_identifiers(self):
        for value in ["1.2.3-rc.0", "1.2.3-0a", "1.2.3-alpha-beta.12", "0.0.0"]:
            with self.subTest(value=value):
                version.parse_version(value)

    def test_channel_tags_and_status(self):
        for environment, suffix, channel in [("canary", "-canary.42", "canary"),
                                              ("beta", "-beta.42", "beta"),
                                              ("production", "", "stable")]:
            with self.subTest(environment=environment):
                result = version.resolve_version("1.2.3", environment, "42")
                self.assertEqual(result["release_tag"], "v1.2.3" + suffix)
                self.assertEqual(result["release_version"], "1.2.3" + suffix)
                self.assertEqual(result["release_channel"], channel)
                self.assertEqual(result["prerelease"], str(bool(suffix)).lower())
                self.assertEqual(result, version.resolve_version("1.2.3", tag=result["release_tag"]))

    def test_invalid_run_and_channel(self):
        for run in [None, "", "0", "01", "-1", "a", "1\nx=y", "1" * 21]:
            with self.subTest(run=run), self.assertRaises(ValueError):
                version.resolve_version("1.2.3", "beta", run)
        with self.assertRaises(ValueError):
            version.resolve_version("1.2.3", "unknown", "1")

    def test_tag_must_match_base(self):
        for tag in ["v1.2.4", "v1.2.4-beta.1", "1.2.3", "v1.2.3-rc.01"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                version.resolve_version("1.2.3", tag=tag)
        self.assertEqual(version.resolve_version("1.2.3", tag="v1.2.3-rc.1")["prerelease"], "true")

    def test_cli_outputs_and_missing_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "VERSION"
            output = Path(directory) / "outputs"
            env = dict(os.environ, GITHUB_OUTPUT=str(output))
            command = [sys.executable, "-B", str(SCRIPTS / "version.py")]
            missing = subprocess.run(command + ["current", "--file", str(path)], capture_output=True, env=env)
            self.assertNotEqual(missing.returncode, 0)
            self.assertFalse(output.exists())
            path.write_text("1.2.3\n")
            result = subprocess.run(command + ["resolve", "--file", str(path), "--environment", "beta",
                                               "--run-number", "42"], capture_output=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"")
            values = dict(line.split("=", 1) for line in output.read_text().splitlines())
            self.assertEqual(values["release_tag"], "v1.2.3-beta.42")
            result = subprocess.run(command + ["bump", "--file", str(path), "--type", "minor"],
                                    capture_output=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"")
            self.assertEqual(path.read_text(), "1.3.0\n")
            path.write_text("bad\n")
            result = subprocess.run(command + ["bump", "--file", str(path)], capture_output=True, env=env)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(path.read_text(), "bad\n")


def pr(number, title="Fallback title", body=None, labels=None):
    return {"number": number, "title": title, "body": body, "labels": labels or [],
            "mergedAt": f"2026-10-{number:02d}T00:00:00Z"}


class ChangelogTests(unittest.TestCase):
    def test_pr_entries_ignore_comments_and_other_sections(self):
        body = "## Description\n- hidden\n## Changelog\n<!--\n- example\n-->\n- feat: add OCR\n  with Vision\n- fix: screenshot\n## Validation\n- hidden\n"
        prs = [pr(2, title="fix: fallback\n title"), pr(1, body=body),
               pr(3, labels=[{"name": "release-request"}]), pr(4, body="## Changelog\n-\n")]
        self.assertEqual(changelog.entries_from_prs(prs), ["- feat: add OCR\n  with Vision",
                         "- fix: screenshot", "- fix: fallback title (#2)", "- Fallback title (#4)"])

    def test_generation_preserves_old_notes(self):
        previous = "# Changelog\n\nIntro.\n\n## [1.2.3]\n\n- fix: old\n"
        updated = changelog.generate(previous, "1.3.0", [pr(1, body="## Changelog\n- feat: new")])
        self.assertIn("# Changelog\n\nIntro.", updated)
        self.assertEqual(changelog.section(updated, "1.3.0"), "- feat: new")
        self.assertEqual(changelog.section(updated, "1.2.3"), "- fix: old")
        with self.assertRaises(ValueError):
            changelog.generate(updated, "1.3.0", [])

    def test_extract_exact_section_not_newer_release(self):
        text = "# Changelog\n\n## [1.3.0]\n\n- new\n\n## [1.2.3] - 2026-10-10\n\n- target\n\n## [1.2.2]\n\n- old\n"
        self.assertEqual(changelog.section(text, "1.2.3"), "- target")
        for bad in [text.replace("[1.2.3]", "[1.2.4]"), "## [1.2.3]\n\n-\n",
                    "## [1.2.3]\n<!-- TODO -->\n", "## [1.2.3]\n- a\n## [1.2.3]\n- b"]:
            with self.subTest(text=bad), self.assertRaises(ValueError):
                changelog.section(bad, "1.2.3")

    def test_empty_and_duplicate_entries(self):
        self.assertEqual(changelog.entries_from_prs([]), ["- chore: maintenance release."])
        self.assertEqual(changelog.entries_from_prs([pr(1, body="## Changelog\n- fix: x"),
                                                    pr(2, body="## Changelog\n- fix: x")]), ["- fix: x"])

    def test_cli_does_not_write_stdout(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "CHANGELOG.md"
            notes = Path(directory) / "notes.md"
            path.write_text("# Changelog\n\n## [1.2.3]\n\n- fix: example\n")
            result = subprocess.run([sys.executable, "-B", str(SCRIPTS / "changelog.py"), "notes",
                                     "--version", "1.2.3", "--changelog", str(path), "--output", str(notes)],
                                    capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"")
            self.assertEqual(notes.read_text(), "- fix: example\n")


if __name__ == "__main__":
    unittest.main()
