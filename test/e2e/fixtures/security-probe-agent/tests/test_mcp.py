"""Unit tests for MCP authorization evidence classification."""

import unittest

from probe.mcp import _mcp_result


class MCPResultTests(unittest.TestCase):
    def test_successful_tool_call_is_authorized(self) -> None:
        result = _mcp_result("echo", "tool_call", 200, True)

        self.assertTrue(result["authorized"])

    def test_non_success_status_is_not_authorized(self) -> None:
        for status in (301, 401, 403, 404, 429, 500):
            with self.subTest(status=status):
                result = _mcp_result("echo", "tool_call", status, False)

                self.assertFalse(result["authorized"])

    def test_non_tool_call_phase_is_not_authorized(self) -> None:
        result = _mcp_result("echo", "initialize", 200, False)

        self.assertFalse(result["authorized"])


if __name__ == "__main__":
    unittest.main()
