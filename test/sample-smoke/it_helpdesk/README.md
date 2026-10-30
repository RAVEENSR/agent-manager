# IT Helpdesk sample smoke tests

These checks use the real sample dependencies, HTTP app, LangGraph agent, and
`check_system_status` tool. An HTTP model stub requests a VPN status lookup,
checks the actual tool output, and only then returns a final answer. No OpenAI,
MCP, or other paid-service credentials are needed. This checks integration
compatibility, not the quality or tool-selection behavior of a real model.

## Local HTTP check

From the repository root, with Python 3.11 or newer:

```bash
PYTHON=python3.11 bash samples/it-helpdesk-agent/scripts/smoke_test.sh
# Optional persistent report location:
PYTHON=python3.11 bash samples/it-helpdesk-agent/scripts/smoke_test.sh --reports /tmp/helpdesk-local
```

The runner creates a disposable venv, installs the checked-out requirements,
runs `pip check`, starts the application and model stub on free loopback ports,
checks `/health`, and calls `/chat`. It verifies the response's session/version
and the model's evidence of the real tool round trip. Application processes and
the venv are removed on exit. Install/application logs and JSON evidence remain
in the printed report directory. Package installation needs network access;
inference does not.

## Agent Manager build, deployment, endpoint, and trace check

Use a running local development stack (`make setup`, see the root guide), its
kubectl context, Python 3.11+, and `requests` installed in your test environment.
The default URLs match the dev-stack action: API `localhost:9000`, Observer
`localhost:9098`, IDP `localhost:8090`. These can be overridden with
`AMP_API_BASE_URL`, `AM_OBSERVER_BASE_URL`, and `IDP_TOKEN_URL`.

```bash
python3.11 -m venv /tmp/helpdesk-test-env
/tmp/helpdesk-test-env/bin/pip install requests
# Use the bootstrap credential for your local dev stack. Never use production credentials.
export IDP_CLIENT_SECRET='<local-dev-client-secret>'
/tmp/helpdesk-test-env/bin/python test/sample-smoke/it_helpdesk/platform_smoke.py \
  --repo https://github.com/wso2/agent-manager \
  --sha <full-published-PR-head-SHA> \
  --reports /tmp/helpdesk-platform
```

Set `--repo` to the fork containing the commit for fork PRs. The commit must be
fetchable from that repository; this command cannot deploy unpublished local
application edits. Local smoke always tests the working tree. The platform test
rejects uncommitted sample application changes to prevent claiming they were
covered by a remote build. A new untracked test-wrapper directory is excluded.

The platform test creates a uniquely named model-stub namespace and Agent
Manager project/agent. The stub runs as a Kubernetes service reachable by the
agent pod, without a service-account token. A temporary NetworkPolicy permits
only this test agent to reach this run's model pod on TCP 8099; sandbox defaults
and other agents' policies remain intact. The policy is removed during cleanup. The agent is created with its full
commit SHA as the source ref; Agent Manager resolves this to a commit-specific
build. Before invocation the test verifies the repository and abbreviated SHA
in the build API, the full SHA in the OpenChoreo WorkflowRun, and that the active
deployment uses that build's image. Missing provenance fails the check.

It probes `/health` on the actual workload via a temporary pod port-forward
(the default chat schema publishes only `/chat`). It then calls the gateway
chat endpoint using a minted agent API key, validates the tool loop against
the stub, and polls Observer for a
model span containing the unique request marker. A trace from an older request
cannot satisfy this assertion. Synthetic prompt content is enabled for this
correlation check. Build provenance, response, model evidence, and matching
trace spans are saved without credentials. Failures identify the phase and
collect cluster state before deleting the test agent, project, and namespace.
The script does not delete shared tracing indices or existing agents.

The test deploys into the pipeline's initial environment (`default` by default).
It requires create/read/delete project and agent permissions, build/deployment
read permissions, API-key management and trace-read permissions, and kubectl
access for its stub and WorkflowRun provenance. It does not bootstrap or alter
a shared stack automatically.

## CI

`IT Helpdesk Sample PR Checks` runs on every PR changing this sample (including
requirements or future Dockerfiles), its smoke harness, or workflow, and can
also be dispatched manually. Both jobs are required to succeed; neither is
advisory. Configure branch protection to require both check names before merging.
Checkout and remote builds use the PR **head SHA**, including
Dependabot PRs. The deployment job uses the existing `amp-dev-stack` action to
create a fresh platform from the checkout and uses its bootstrap test credential;
it requires no Actions secrets. Existing nightly/platform suites are unchanged.
Before stack setup, the platform job runs `preflight-k3d` to check runner
capacity, required ports, and whether Docker-published ports are reachable from
the runner. On failure it uploads bounded Docker/k3s logs, container state and
network mappings, runner network information, and Kubernetes readiness/errors
in the `it-helpdesk-platform-smoke` artifact, even if Kubernetes is unreachable.
The artifact's `preflight/` directory also retains image-pull errors, container
startup output, and probe state/logs captured before cleanup. Pull and startup
failures are reported separately; a cached probe image requires no registry pull.

The platform job is intentionally heavier than local smoke: it provisions a
cluster and builds the platform before testing one agent.

Run regression guards independently:

```bash
/tmp/helpdesk-test-env/bin/python -m unittest discover \
  -s test/sample-smoke/it_helpdesk -p 'test_*.py' -v
```
