"""Exercise the real rollback function with mocked service commands, never systemd."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest


class InstallerRollback(unittest.TestCase):
    def test_failed_restart_restores_previous_binary(self):
        source = (Path(__file__).resolve().parents[1] / "deploy/install.sh").read_text(encoding="utf-8")
        function = source[source.index("activate_release() {"):source.index("# A crash loop")]
        bash = shutil.which("bash")
        git_bash = Path("C:/Program Files/Git/bin/bash.exe")
        if os.name == "nt" and git_bash.exists():
            bash = str(git_bash)
        self.assertIsNotNone(bash, "bash is required for installer tests")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for directory in ("current", "releases/1.0.0"):
                target = root / directory
                target.mkdir(parents=True)
                (target / "lightspeak-server").touch()
            # File mutations and service calls are mocked. Only shell control flow
            # and the checked-in rollback implementation execute.
            harness = r'''
set -euo pipefail
APP_DIR=$1
STAGE=unused
NEW_VER=1.1.0
DATA=$1
ADMIN_BIN=true
say() { :; }
warn() { echo "$*"; }
die() { echo "$*"; exit 1; }
readlink() { echo releases/1.0.0; }
stat() { echo root:root; }
rm() { :; }
mv() { :; }
chown() { :; }
journalctl() { :; }
switch_release() { echo "SWITCH:$1"; }
attempt=0
restart_server() { attempt=$((attempt+1)); test "$attempt" -gt 1; }
healthy() { return 0; }
'''
            result = subprocess.run(
                [bash, "-c", harness + function + "\nactivate_release\n", "test", root.as_posix()],
                capture_output=True, text=True, timeout=15,
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn("SWITCH:releases/1.0.0", result.stdout)
            self.assertIn("database migrations were NOT undone", result.stdout)


if __name__ == "__main__":
    unittest.main()
