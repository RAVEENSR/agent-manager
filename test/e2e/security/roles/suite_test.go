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

// Package roles verifies the deployed Thunder role-to-scope policy by using
// disposable OAuth applications as non-interactive role personas.
package roles

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
)

var (
	cfg         *framework.Config
	provisioner *framework.PersonaProvisioner
	personas    map[string]*framework.RolePersona
)

var roleNames = []string{"admin", "developer", "ai-lead", "platform-engineer"}

var deployedRoleNames = map[string]string{
	"admin":             "Agent Manager Admin",
	"developer":         "Developer",
	"ai-lead":           "AI Lead",
	"platform-engineer": "Platform Engineer",
}

func TestSecurityRoles(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Security: Role Personas Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	cfg = framework.LoadConfig()
	framework.WaitForAPIReady(cfg)

	var err error
	provisioner, err = framework.NewPersonaProvisioner(ctx, cfg)
	Expect(err).NotTo(HaveOccurred(),
		"role-persona tests require Thunder system-client access; configure THUNDER_ADMIN_URL, "+
			"THUNDER_SYSTEM_CLIENT_ID, and THUNDER_SYSTEM_CLIENT_SECRET")

	personas = make(map[string]*framework.RolePersona, len(roleNames))
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, persona := range personas {
			if persona == nil {
				continue
			}
			Expect(provisioner.DeleteRolePersona(cleanupCtx, persona)).To(Succeed(),
				"failed to remove disposable Thunder persona %s", persona.Name)
		}
	})

	for _, roleName := range roleNames {
		deployedName := deployedRoleNames[roleName]
		By("Provisioning disposable " + deployedName + " persona")
		personas[roleName], err = provisioner.CreateRolePersona(ctx, deployedName)
		Expect(err).NotTo(HaveOccurred())
	}
})
