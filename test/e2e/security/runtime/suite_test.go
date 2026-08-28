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

// Package runtime verifies the security boundary around a real platform-hosted
// agent, including sandbox containment and its injected AgentID.
package runtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
	"github.com/wso2/agent-manager/test/e2e/operations/gateway"
)

var (
	cfg         *framework.Config
	adminClient *framework.AMPClient
	suffix      string
	agentName   string
	proxyID     string
	envUUID     string
)

func TestSecurityRuntime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Security: Deployed Agent Runtime Suite")
}

var _ = BeforeSuite(func() {
	cfg = framework.LoadConfig()

	By("Waiting for Agent Manager readiness")
	framework.WaitForAPIReady(cfg)

	By("Creating the full-privilege runtime fixture client")
	var err error
	adminClient, err = framework.NewAMPClient(cfg)
	Expect(err).NotTo(HaveOccurred())
	framework.VerifyDefaultOrg(adminClient, cfg.DefaultOrg)

	By("Waiting for the default environment gateway")
	_, envUUID = gateway.WaitForActiveGatewayForEnvWithEnvUUID(
		adminClient, cfg.DefaultOrg, cfg.DefaultEnv, 3*time.Minute)
	Expect(envUUID).NotTo(BeEmpty())

	suffix = uuid.NewString()[:8]
	agentName = "e2e-sec-runtime-" + suffix
	proxyID = "e2e-sec-mcp-" + suffix
})
