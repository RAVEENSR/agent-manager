// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Validates the full lifecycle of the single-environment IT helpdesk agent
// as explicit test steps: create, build, deploy, become ready,
// invoke, metrics, and traces. This is the canonical build of the shared IT helpdesk agent; the
// configuration, llmprovider, monitors and traces domains reuse the same agent
// (by name, via testsetup.SetupSharedITHelpdeskAgent) rather than rebuilding it.

package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
	agentops "github.com/wso2/agent-manager/test/e2e/operations/agent"
	"github.com/wso2/agent-manager/test/e2e/operations/build"
	"github.com/wso2/agent-manager/test/e2e/operations/deployment"
	traceops "github.com/wso2/agent-manager/test/e2e/operations/trace"
)

var _ = Describe("Internal chat agent in default environment:", Label("agent", "internal-agent"), Ordered, func() {
	const (
		projectName = framework.E2ESharedProjectName
		agentName   = framework.SharedITHelpdeskAgentName
	)

	var (
		endpointURL string
		invokeReq   json.RawMessage
	)

	It("creates an internal IT-helpdesk chat agent from its source repository", func() {
		Expect(Cfg.OpenAIAPIKey).NotTo(BeEmpty(), "OPENAI_API_KEY must be set")

		// The shared project is created once in the suite's SynchronizedBeforeSuite.
		agentPath := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s", Cfg.DefaultOrg, projectName, agentName)
		if framework.ResourceExists(Client, agentPath) {
			GinkgoWriter.Printf("Agent already exists, reusing: %s\n", agentName)
			return
		}
		ag := agentops.CreateAgent(Default, Client, &agentops.CreateAgentParams{
			OrgName:     Cfg.DefaultOrg,
			ProjectName: projectName,
			Request: framework.NewITHelpdeskAgentRequest(
				agentName,
				"Internal IT-helpdesk chat agent used by the single-environment agent lifecycle e2e tests",
				map[string]string{
					"OPENAI_API_KEY": Cfg.OpenAIAPIKey,
					"DATABASE_URL":   "http://localhost:5000",
				},
			),
		})
		Expect(ag.Name).To(Equal(agentName))
		GinkgoWriter.Printf("Agent created: %s\n", agentName)
	})

	It("builds the agent image to completion", func() {
		buildName := build.WaitForBuildSuccess(Client, &build.WaitForBuildParams{
			OrgName:     Cfg.DefaultOrg,
			ProjectName: projectName,
			AgentName:   agentName,
			Timeout:     20 * time.Minute,
		})
		GinkgoWriter.Printf("Build completed: %s\n", buildName)
	})

	It("deploys the agent to the default environment", func() {
		deployment.WaitForDeployed(Client, &deployment.WaitForDeploymentParams{
			OrgName:     Cfg.DefaultOrg,
			ProjectName: projectName,
			AgentName:   agentName,
			Environment: Cfg.DefaultEnv,
			Timeout:     5 * time.Minute,
		})
		GinkgoWriter.Printf("Agent deployed: %s\n", agentName)
	})

	It("becomes ready and exposes an invocation endpoint", func() {
		deployment.WaitForReadiness(Client, Cfg.DefaultOrg, projectName, agentName, Cfg.DefaultEnv, 10*time.Minute)

		endpoints := deployment.GetEndpoints(Default, Client,
			Cfg.DefaultOrg, projectName, agentName, Cfg.DefaultEnv)
		endpointURL = deployment.FirstEndpointURL(endpoints)
		Expect(endpointURL).NotTo(BeEmpty(), "agent endpoint URL should not be empty")

		// The path is the agent name alone. It used to be
		// "<agent>-<agent>-endpoint", because the ComponentType prefixed the
		// component name onto an endpoint name that already carried it.
		// This assumes the shared agent above was created (not merely reused)
		// by a service version that writes routePath at create time; against a
		// long-lived local environment whose shared agent predates that change,
		// this assertion fails even though the code under test is correct.
		parsed, err := url.Parse(endpointURL)
		Expect(err).NotTo(HaveOccurred(), "endpoint URL should parse")
		Expect(parsed.Path).To(Equal("/"+agentName),
			"expected the invoke path to be the agent name alone, got %q", parsed.Path)

		invokeReq = framework.DefaultInvokeRequest()
		GinkgoWriter.Printf("Agent ready: endpoint=%s\n", endpointURL)
	})

	It("returns a chat response when invoked", func() {
		// Create the API key in the same It as the invocation so its DeferCleanup
		// revoke runs only after the invocation completes (keeps key count bounded).
		apiKeyResp := agentops.CreateAgentAPIKey(Default, Client,
			Cfg.DefaultOrg, projectName, agentName, Cfg.DefaultEnv,
			framework.CreateAgentAPIKeyRequest{
				DisplayName: "e2e-test-key",
				ExpiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			})
		Expect(apiKeyResp.ApiKey).NotTo(BeEmpty(), "agent API key should not be empty")

		agentops.InvokeAgentEndpoint(endpointURL+"/chat", invokeReq, apiKeyResp.ApiKey)
	})

	It("exposes CPU and memory metrics for the running agent", func() {
		metrics := agentops.GetMetrics(Default, Client,
			Cfg.DefaultOrg, projectName, agentName, Cfg.DefaultEnv)
		Expect(metrics.CPUUsage).NotTo(BeEmpty(), "expected CPU usage metrics")
		Expect(metrics.Memory).NotTo(BeEmpty(), "expected memory metrics")
		GinkgoWriter.Printf("CPU points: %d, Memory points: %d\n", len(metrics.CPUUsage), len(metrics.Memory))
	})

	It("captures a trace for the agent invocation", func() {
		traces := traceops.WaitForTraces(Client, &traceops.WaitForTracesParams{
			Organization: Cfg.DefaultOrg,
			Project:      projectName,
			Agent:        agentName,
			Environment:  Cfg.DefaultEnv,
			Timeout:      2 * time.Minute,
		})
		Expect(traces.Traces).NotTo(BeEmpty(), "expected at least one trace after agent invocation")
		GinkgoWriter.Printf("Traces: %d found\n", len(traces.Traces))
	})
})
