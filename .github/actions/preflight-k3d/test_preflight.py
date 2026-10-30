"""Exercise preflight failure evidence and cleanup without a Docker daemon."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("preflight.sh")

DOCKER = r'''printf '%s\n' "$*" >> "$CALLS"
case "$1" in
  info) echo "fixture daemon" ;;
  context) echo "default" ;;
  image) [ "$MODE" = cached ] ;;
  pull)
    if [ "$MODE" = pull ]; then echo "fixture registry denied" >&2; exit 1; fi
    echo "fixture image pulled" ;;
  run)
    if [ "$MODE" = start ]; then echo "fixture bind failed" >&2; exit 125; fi
    echo "fixture container ID" ;;
  inspect) echo 'State={"Status":"exited","ExitCode":1} Ports={}' ;;
  logs) echo "fixture container diagnostic" ;;
  rm) echo "fixture removed" ;;
esac
'''


class PreflightDiagnostics(unittest.TestCase):
    def run_preflight(self, mode):
        temporary = tempfile.TemporaryDirectory(prefix="preflight-test-")
        self.addCleanup(temporary.cleanup)
        work = Path(temporary.name)
        binaries = work / "bin"
        binaries.mkdir()
        stubs = {
            "docker": DOCKER,
            "timeout": 'shift\nexec "$@"',
            "curl": '[ "$MODE" != unreachable ]',
            "sleep": 'exit 0',
            "lsof": 'exit 1',
            "nproc": 'echo 8',
            "df": 'printf "Avail\\n100G\\n"',
        }
        for name, body in stubs.items():
            executable = binaries / name
            executable.write_text("#!/bin/bash\n" + body + "\n")
            executable.chmod(0o755)
        reports = work / "reports"
        calls = work / "calls.txt"
        env = {**os.environ, "PATH": str(binaries) + ":" + os.environ["PATH"],
               "MODE": mode, "CALLS": str(calls), "PROBE_REPORT_DIR": str(reports),
               "MIN_CPUS": "0", "MIN_MEM_GB": "0", "MIN_DISK_GB": "0",
               "ENFORCE_CAPACITY": "false", "REQUIRED_PORTS": "6550"}
        result = subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True, timeout=10)
        return result, reports, calls.read_text().splitlines()

    def test_pull_error_is_retained_and_reported(self):
        result, reports, calls = self.run_preflight("pull")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("fixture registry denied", result.stdout + result.stderr)
        self.assertIn("fixture registry denied", (reports / "image-pull.log").read_text())
        self.assertFalse(any(call.startswith("run ") for call in calls))

    def test_start_error_and_container_logs_precede_cleanup(self):
        result, reports, calls = self.run_preflight("start")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("fixture bind failed", result.stdout + result.stderr)
        self.assertIn("fixture bind failed", (reports / "container-start.log").read_text())
        self.assertIn("fixture container diagnostic", (reports / "container.log").read_text())
        self.assertIn('"ExitCode":1', (reports / "container-state.txt").read_text())
        self.assertLess(next(i for i, call in enumerate(calls) if call.startswith("logs ")), max(i for i, call in enumerate(calls) if call.startswith("rm ")))
        self.assertNotIn("--rm", next(call for call in calls if call.startswith("run ")))

    def test_unreachable_port_still_fails_and_retains_state(self):
        result, reports, calls = self.run_preflight("unreachable")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("NOT reachable", result.stdout)
        self.assertTrue((reports / "container-state.txt").exists())
        self.assertTrue(calls[-1].startswith("rm "))

    def test_success_cleans_up_and_passes(self):
        result, reports, calls = self.run_preflight("success")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Preflight passed", result.stdout)
        self.assertTrue((reports / "container.log").exists())
        self.assertTrue(calls[-1].startswith("rm "))

    def test_cached_image_does_not_require_registry_access(self):
        result, _, calls = self.run_preflight("cached")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(any(call.startswith("pull ") for call in calls))


if __name__ == "__main__":
    unittest.main()
