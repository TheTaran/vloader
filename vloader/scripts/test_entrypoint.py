"""Exercise startup dispatch with isolated fake mount commands; never mount the host."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ENTRYPOINT = Path(__file__).resolve().parents[1] / "docker/entrypoint.sh"


class EntrypointTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.env = dict(os.environ, PATH=str(self.bin) + ":" + os.environ["PATH"],
                        SOURCE_MOUNT="none", FAKE_UID="0", MOUNT_FAIL="0",
                        MOUNT_MARKER=str(self.root / "mounted"), LOG=str(self.root / "log"),
                        SMB_CREDENTIALS_PATH=str(self.root / "credentials"))
        (self.root / "credentials").write_text("username=test\npassword=never-log-this\n")
        self.command("id", 'echo "$FAKE_UID"')
        self.command("mountpoint", '[ -f "$MOUNT_MARKER" ]')
        for helper in ("mount.nfs", "mount.cifs"):
            self.command(helper, 'printf "%s\\n" "$0" "$@" >> "$LOG"; [ "$MOUNT_FAIL" = 0 ] || exit 1; touch "$MOUNT_MARKER"')
        self.command("timeout", 'shift; exec "$@"')
        self.command("su-exec", 'echo "drop:$1" >> "$LOG"; shift; exec "$@"')
        self.command("app", 'echo "app:${SOURCE_MODE:-emby}" >> "$LOG"')

    def command(self, name, body):
        file = self.bin / name
        file.write_text("#!/bin/sh\n" + body + "\n")
        file.chmod(0o755)

    def run_start(self, **values):
        return subprocess.run(["sh", str(ENTRYPOINT), "app"], env=dict(self.env, **values), capture_output=True, text=True)

    def log(self):
        file = self.root / "log"
        return file.read_text() if file.exists() else ""

    def test_default_does_not_mount_and_drops_root(self):
        self.assertEqual(self.run_start().returncode, 0)
        self.assertEqual(self.log(), "drop:10001:10001\napp:emby\n")

    def test_nonroot_default_runs_without_uid_switch(self):
        self.assertEqual(self.run_start(FAKE_UID="10001").returncode, 0)
        self.assertEqual(self.log(), "app:emby\n")

    def test_nfs_mount_is_readonly_and_precedes_privilege_drop(self):
        result = self.run_start(SOURCE_MOUNT="nfs", NFS_SERVER="nas.local", NFS_EXPORT="/films with spaces")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("nas.local:/films with spaces\n/media\n", self.log())
        self.assertIn("ro,nosuid,nodev,noexec,nfsvers=4", self.log())
        self.assertTrue(self.log().endswith("drop:10001:10001\napp:mount\n"))

    def test_smb_uses_secret_file_not_password_arguments(self):
        result = self.run_start(SOURCE_MOUNT="smb", SMB_SERVER="nas.local", SMB_SHARE="Movie Share")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("//nas.local/Movie Share\n/media\n", self.log())
        self.assertIn("vers=3.1.1,ro,nosuid,nodev,noexec", self.log())
        self.assertNotIn("never-log-this", self.log() + result.stdout + result.stderr)
        self.assertTrue(self.log().endswith("drop:10001:10001\napp:mount\n"))

    def test_failed_mount_never_starts_app(self):
        result = self.run_start(SOURCE_MOUNT="nfs", NFS_SERVER="nas.local", NFS_EXPORT="/films", MOUNT_FAIL="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("app:", self.log())

    def test_missing_credentials_and_nonroot_mount_rejected(self):
        for values in (
            dict(SOURCE_MOUNT="smb", SMB_SERVER="nas", SMB_SHARE="films", SMB_CREDENTIALS_PATH="/missing"),
            dict(SOURCE_MOUNT="nfs", NFS_SERVER="nas", NFS_EXPORT="/films", FAKE_UID="10001"),
            dict(SOURCE_MOUNT="invalid"),
            dict(SOURCE_MOUNT="nfs", NFS_SERVER="-o,rw", NFS_EXPORT="/films"),
            dict(SOURCE_MOUNT="smb", SMB_SERVER="nas", SMB_SHARE="films", SMB_CREDENTIALS_PATH="/tmp/file,rw"),
        ):
            with self.subTest(values=values):
                self.assertNotEqual(self.run_start(**values).returncode, 0)
                self.assertNotIn("app:", self.log())

    def test_existing_mount_never_overmounted(self):
        (self.root / "mounted").touch()
        result = self.run_start(SOURCE_MOUNT="nfs", NFS_SERVER="nas.local", NFS_EXPORT="/films")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.log(), "")


if __name__ == "__main__":
    unittest.main()
