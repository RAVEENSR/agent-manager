"""Tests for fail-closed network-isolation evidence classification."""

import unittest

import httpx

from probe.network import _verified_cluster_api_host, classify_network_failure


class NetworkFailureClassificationTests(unittest.TestCase):
    def test_injected_literal_cluster_api_host_is_verified(self) -> None:
        self.assertTrue(_verified_cluster_api_host("10.43.0.1", "10.43.0.1"))

    def test_fallback_hostname_is_not_verified(self) -> None:
        self.assertFalse(_verified_cluster_api_host("kubernetes.default.svc", None))

    def test_injected_hostname_is_not_treated_as_known_live_ip(self) -> None:
        self.assertFalse(
            _verified_cluster_api_host(
                "kubernetes.default.svc",
                "kubernetes.default.svc",
            )
        )

    def test_connect_timeout_is_blocked(self) -> None:
        self.assertEqual(
            classify_network_failure(
                httpx.ConnectTimeout("redacted"),
                verified_cluster_target=True,
            ),
            ("blocked", "connect_timeout"),
        )

    def test_known_live_cluster_target_rejection_is_blocked(self) -> None:
        self.assertEqual(
            classify_network_failure(
                httpx.ConnectError("redacted"),
                verified_cluster_target=True,
            ),
            ("blocked", "connect_rejected"),
        )

    def test_unverified_target_connect_error_is_not_a_pass(self) -> None:
        self.assertEqual(
            classify_network_failure(
                httpx.ConnectError("redacted"),
                verified_cluster_target=False,
            ),
            ("indeterminate", "unverified_connect_error"),
        )

    def test_post_connect_timeout_is_not_a_pass(self) -> None:
        self.assertEqual(
            classify_network_failure(
                httpx.ReadTimeout("redacted"),
                verified_cluster_target=True,
            ),
            ("indeterminate", "read_timeout"),
        )

    def test_pool_timeout_is_not_network_policy_evidence(self) -> None:
        self.assertEqual(
            classify_network_failure(
                httpx.PoolTimeout("redacted"),
                verified_cluster_target=True,
            ),
            ("indeterminate", "pool_timeout"),
        )


if __name__ == "__main__":
    unittest.main()
