import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

WORKFLOW = (Path(__file__).resolve().parents[1] / ".github/workflows/docker-image.yml").read_text()


def run_step(name, values):
    section = WORKFLOW.split("      - name: " + name + "\n", 1)[1].split("\n      - name:", 1)[0]
    script = textwrap.dedent(section.split("        run: |\n", 1)[1])
    with tempfile.NamedTemporaryFile() as out:
        env = dict(os.environ, GITHUB_OUTPUT=out.name, **values)
        result = subprocess.run(["bash", "-e", "-c", script], env=env, capture_output=True, text=True)
        return result.returncode, Path(out.name).read_text()


class ReleaseWorkflowTest(unittest.TestCase):
    def classify(self, event="push", ref_type="branch", ref="main", tag=""):
        return run_step("Classify release", {"GITHUB_EVENT_NAME": event, "GITHUB_REF_TYPE": ref_type, "GITHUB_REF_NAME": ref, "REQUESTED_TAG": tag})

    def test_only_explicit_release_events_publish(self):
        for event in ("push", "pull_request", "workflow_dispatch"):
            self.assertIn("is_release=false", self.classify(event=event)[1])
        self.assertIn("is_release=false", self.classify(event="pull_request", ref_type="tag", ref="v1.2")[1])
        self.assertIn("tag=v1.2.3", self.classify(event="workflow_dispatch", tag="v1.2.3")[1])
        self.assertIn("tag=v1.2", self.classify(ref_type="tag", ref="v1.2")[1])

    def test_invalid_and_injected_tags_fail(self):
        for tag in ("v1.2.3-rc1", "v01.2", "v1.2\nis_release=true", "$(echo injected)", "main", "1.2"):
            self.assertNotEqual(0, self.classify(event="workflow_dispatch", tag=tag)[0], tag)

    def test_master_minor_and_build_tags(self):
        common = {"IMAGE_NAME": "ghcr.io/example/vloader", "SOURCE_SHA": "abcdef123456"}
        for tag, expected, absent in (("v1.2", ":latest", ":1.2.0"), ("v1.2.3", ":1.2\n", ":latest")):
            code, output = run_step("Prepare Docker tags", dict(common, IS_RELEASE="true", RELEASE_TAG=tag))
            self.assertEqual(code, 0)
            self.assertIn(expected, output)
            self.assertNotIn(absent, output)
            self.assertIn(":sha-abcdef1", output)
        code, output = run_step("Prepare Docker tags", dict(common, IS_RELEASE="false", RELEASE_TAG=""))
        self.assertEqual(code, 0)
        self.assertNotIn(":latest", output)


if __name__ == "__main__":
    unittest.main()
