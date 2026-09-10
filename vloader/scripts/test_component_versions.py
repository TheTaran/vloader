import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("component_versions", Path(__file__).with_name("check-component-versions.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class ComponentVersionsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "Dockerfile").write_text("FROM golang:1.27.1-alpine AS build\nFROM alpine:3.24\n")
        (self.root / "go.mod").write_text("module test\n\ngo 1.27.1\nrequire (\n example.com/library v1.2.3\n)\nrequire example.com/indirect v2.0.0 // indirect\n")

    def fetch(self, url):
        if "go.dev" in url:
            return [{"version": "go1.28rc1", "stable": False}, {"version": "go1.27.1", "stable": True}]
        if "alpinelinux" in url:
            return {"release_branches": [{"rel_branch": "edge"}, {"rel_branch": "v3.25", "releases": []}, {"rel_branch": "v3.24", "releases": [{"version": "3.24.1"}]}]}
        return {"Version": "v2.0.0" if "indirect" in url else "v1.2.3"}

    def test_current_ignores_prerelease_and_unreleased_alpine(self):
        updates, body = checker.check(self.root, self.fetch)
        self.assertFalse(updates)
        self.assertIn("example.com/indirect", body)
        self.assertNotIn("1.28rc1", body)
        self.assertNotIn("3.25", body)

    def test_numeric_version_order(self):
        self.assertGreater(checker.version("v1.10.0"), checker.version("v1.9.9"))
        with self.assertRaises(ValueError):
            checker.version("v1.11.0-rc1")

    def test_module_update_and_go_mismatch(self):
        def newer(url):
            return {"Version": "v1.3.0"} if "library" in url else self.fetch(url)
        self.assertTrue(checker.check(self.root, newer)[0])
        (self.root / "Dockerfile").write_text("FROM golang:1.27.0-alpine AS build\nFROM alpine:3.24\n")
        updates, body = checker.check(self.root, self.fetch)
        self.assertTrue(updates)
        self.assertIn("Go version mismatch", body)

    def test_partial_upstream_failure_fails_check(self):
        def broken(url):
            if "indirect" in url:
                raise OSError("upstream unavailable")
            return self.fetch(url)
        with self.assertRaises(OSError):
            checker.check(self.root, broken)

    @patch.dict("os.environ", {"REPOSITORY": "example/vloader"})
    @patch.object(checker.subprocess, "check_output")
    def test_unrelated_issue_not_modified(self, run):
        run.return_value = '[{"number":1,"title":"Component updates available","body":"Human report"}]'
        checker.manage_issue(False, "body")
        self.assertEqual(run.call_count, 1)

    @patch.dict("os.environ", {"REPOSITORY": "example/vloader"})
    @patch.object(checker.subprocess, "check_output")
    def test_managed_issue_closed_when_current(self, run):
        run.side_effect = ['[{"number":2,"title":"Component updates available","body":"<!-- vloader-component-versions -->"}]', ""]
        checker.manage_issue(False, "body")
        self.assertIn("close", run.call_args.args[0])
        self.assertIn("2", run.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
