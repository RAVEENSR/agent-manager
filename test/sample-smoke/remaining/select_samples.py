"""Select PR-head sample checks from changed paths; shared harness edits run all."""

import json
import os
import subprocess

SAMPLES = (
    "crewai-agent",
    "customer-support-agent",
    "dotnet-agent",
    "leave-assistant-agent",
    "manual-instrumentation-agent",
)
WORKFLOW = ".github/workflows/remaining-samples-pr-checks.yaml"


def select(paths):
    if any(
        p.startswith("test/sample-smoke/remaining/") or p == WORKFLOW for p in paths
    ):
        return list(SAMPLES)
    return [
        sample
        for sample in SAMPLES
        if any(p.startswith(f"samples/{sample}/") for p in paths)
    ]


if __name__ == "__main__":
    if os.environ["GITHUB_EVENT_NAME"] == "workflow_dispatch":
        samples = list(SAMPLES)
    else:
        base = os.environ["BASE_SHA"]
        subprocess.run(
            [
                "git",
                "fetch",
                "--no-tags",
                "--depth=1",
                "https://github.com/" + os.environ["GITHUB_REPOSITORY"],
                base,
            ],
            check=True,
        )
        paths = subprocess.check_output(
            ["git", "diff", "--name-only", base, "HEAD"], text=True
        ).splitlines()
        samples = select(paths)
    with open(os.environ["GITHUB_OUTPUT"], "a") as out:
        out.write("samples=" + json.dumps(samples) + "\n")
        out.write("any=" + str(bool(samples)).lower() + "\n")
    print("Selected samples:", ", ".join(samples) or "none")
