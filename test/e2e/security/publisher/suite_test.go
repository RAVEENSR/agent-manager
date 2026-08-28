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

// Package publisher verifies the special amp-publisher-* audience boundary
// shared by Agent Manager score ingestion and Observer trace access.
package publisher

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
)

var (
	cfg             *framework.Config
	provisioner     *framework.PersonaProvisioner
	publisherClient *framework.AudienceClient
	lookalikeClient *framework.AudienceClient
	normalClient    *framework.AMPClient
	observerClient  = &http.Client{Timeout: 30 * time.Second}
)

func TestSecurityPublisher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Security: Publisher Audience Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	cfg = framework.LoadConfig()

	By("Waiting for Agent Manager readiness")
	framework.WaitForAPIReady(cfg)

	By("Checking Observer readiness")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, observerURL("/api/v1/traces"), nil)
	Expect(err).NotTo(HaveOccurred())
	resp, err := observerClient.Do(req)
	Expect(err).NotTo(HaveOccurred(), "cannot reach Observer at %s", cfg.ObserverBaseURL)
	resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized),
		"Observer data routes must reject unauthenticated requests before this suite can be trusted")

	By("Authenticating the Thunder system client")
	provisioner, err = framework.NewPersonaProvisioner(ctx, cfg)
	Expect(err).NotTo(HaveOccurred(),
		"publisher tests require THUNDER_ADMIN_URL, THUNDER_SYSTEM_CLIENT_ID, and THUNDER_SYSTEM_CLIENT_SECRET")

	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		Expect(provisioner.DeleteAudienceClient(cleanupCtx, publisherClient)).To(Succeed())
		Expect(provisioner.DeleteAudienceClient(cleanupCtx, lookalikeClient)).To(Succeed())
	})

	By("Creating a valid amp-publisher-* OAuth client")
	publisherClient, err = provisioner.CreateAudienceClient(ctx, "amp-publisher-e2e-test-sec-")
	Expect(err).NotTo(HaveOccurred())

	By("Creating a publisher lookalike OAuth client")
	lookalikeClient, err = provisioner.CreateAudienceClient(ctx, "e2e-test-sec-fake-amp-publisher-")
	Expect(err).NotTo(HaveOccurred())

	By("Creating the normal AMP API control client")
	normalClient, err = framework.NewAMPClientWithContext(ctx, cfg)
	Expect(err).NotTo(HaveOccurred())
})

func observerURL(path string) string {
	return strings.TrimSuffix(cfg.ObserverBaseURL, "/") + path
}

func observerGet(ctx SpecContext, token, path string) *http.Response {
	resp, err := framework.NewAMPClientWithToken(cfg, token).
		DoRawWithContext(ctx, http.MethodGet, observerURL(path))
	Expect(err).NotTo(HaveOccurred(), "Observer request failed: %s", path)
	return resp
}
