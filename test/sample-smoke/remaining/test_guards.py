import unittest

from model_stub import ModelServer
from select_samples import SAMPLES, select


class Guards(unittest.TestCase):
    def test_each_dependency_ecosystem_selects_its_sample(self):
        for sample, file in [
            ("crewai-agent", "requirements.txt"),
            ("customer-support-agent", "requirements.txt"),
            ("manual-instrumentation-agent", "requirements.txt"),
            ("dotnet-agent", "dotnet-agent.csproj"),
            ("dotnet-agent", "Dockerfile"),
            ("leave-assistant-agent", "Dependencies.toml"),
        ]:
            self.assertEqual(select([f"samples/{sample}/{file}"]), [sample])

    def test_shared_changes_select_all(self):
        self.assertEqual(
            select(["test/sample-smoke/remaining/model_stub.py"]), list(SAMPLES)
        )

    def test_completed_samples_are_excluded(self):
        self.assertEqual(
            select(
                [
                    "samples/a2a-agent/requirements.txt",
                    "samples/hotel-booking-agent/agent/requirements.txt",
                    "samples/insurance-support-agent/agent/requirements.txt",
                    "samples/it-helpdesk-agent/requirements.txt",
                ]
            ),
            [],
        )

    def test_missing_or_wrong_tool_output_cannot_pass(self):
        with ModelServer("dotnet-agent") as model:
            with self.assertRaises(AssertionError):
                model.completion(
                    {
                        "model": "test",
                        "messages": [{"role": "tool", "content": "wrong output"}],
                    }
                )
            self.assertFalse(model.verified)
            response = model.completion(
                {
                    "model": "test",
                    "messages": [{"role": "user", "content": "weather"}],
                    "tools": [{"function": {"name": "GetWeather"}}],
                }
            )
            self.assertEqual(response["choices"][0]["finish_reason"], "tool_calls")
            self.assertFalse(model.verified)


if __name__ == "__main__":
    unittest.main()
