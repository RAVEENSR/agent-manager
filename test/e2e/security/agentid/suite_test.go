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

// Package agentid verifies the credential lifecycle for the per-environment
// OAuth2 identities assigned to agents.
package agentid

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
	environmentops "github.com/wso2/agent-manager/test/e2e/operations/environment"
)

var (
	cfg           *framework.Config
	adminClient   *framework.AMPClient
	reducedClient *framework.AMPClient
)

func TestSecurityAgentID(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Security: AgentID Credential Lifecycle Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	cfg = framework.LoadConfig()

	By("Waiting for Agent Manager readiness")
	framework.WaitForAPIReady(cfg)

	By("Creating the full-privilege fixture client")
	var err error
	adminClient, err = framework.NewAMPClientWithContext(ctx, cfg)
	Expect(err).NotTo(HaveOccurred())
	framework.VerifyDefaultOrg(adminClient, cfg.DefaultOrg)

	if cfg.AgentIDTokenURL == "" {
		By("Discovering the environment-specific Thunder token endpoint")
		Eventually(func(g Gomega) string {
			instances := environmentops.ListThunderInstances(ctx, g, adminClient, cfg.DefaultOrg)
			for _, instance := range instances.ThunderInstances {
				if instance.EnvName == cfg.DefaultEnv {
					cfg.AgentIDTokenURL = instance.TokenURL
					return instance.TokenURL
				}
			}
			return ""
		}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(3*time.Second).
			ShouldNot(BeEmpty(), "no reachable environment Thunder instance was registered for %s", cfg.DefaultEnv)
	}

	By("Creating a valid AMP token without amp:agent:update")
	reducedToken, err := framework.FetchTokenWithScopes(ctx, cfg, []string{"amp:agent:read"})
	Expect(err).NotTo(HaveOccurred())
	scopes, err := framework.TokenScopes(reducedToken)
	Expect(err).NotTo(HaveOccurred())
	Expect(scopes).To(HaveKey("amp:agent:read"),
		"the reduced token needs an AMP permission so denial is measured at authorization, not audience validation")
	Expect(scopes).NotTo(HaveKey("amp:agent:update"),
		"the AgentID negative control unexpectedly received the permission under test")
	reducedClient = framework.NewAMPClientWithToken(cfg, reducedToken)
})
