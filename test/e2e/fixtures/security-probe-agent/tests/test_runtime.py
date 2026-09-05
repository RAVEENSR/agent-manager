"""Tests for fail-closed runtime-hardening evidence classification."""

import unittest

from probe.runtime import classify_no_new_privileges


class NoNewPrivilegesClassificationTests(unittest.TestCase):
    def test_prctl_enabled_is_authoritative(self) -> None:
        self.assertEqual(
            classify_no_new_privileges(1, "0"),
            (True, "prctl_enabled"),
        )

    def test_prctl_disabled_fails(self) -> None:
        self.assertEqual(
            classify_no_new_privileges(0, "1"),
            (False, "prctl_disabled"),
        )

    def test_proc_enabled_is_only_diagnostic_when_prctl_is_unavailable(self) -> None:
        self.assertEqual(
            classify_no_new_privileges(None, "1"),
            (False, "prctl_unavailable_proc_enabled"),
        )

    def test_missing_prctl_and_proc_evidence_fails_closed(self) -> None:
        self.assertEqual(
            classify_no_new_privileges(None, None),
            (False, "prctl_unavailable_proc_missing"),
        )


if __name__ == "__main__":
    unittest.main()
