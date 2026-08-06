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

// Package environment provides e2e operations for managing environments,
// including running the operator-facing add/remove environment shell scripts
// that provision the API Platform Gateway via helm.
package environment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wso2/agent-manager/test/e2e/framework"
)

// ScriptParams are the inputs for the add/remove environment scripts.
type ScriptParams struct {
	OrgName      string
	EnvName      string
	DisplayName  string
	Token        string
	AMPBaseURL   string
	IsProduction bool

	// Topology overrides the ambient GATEWAY_TOPOLOGY env var when set (e.g.
	// "split" to provision separate INGRESS/EGRESS gateways). Leave blank to
	// use the ambient value, defaulting to "single".
	Topology string

	// Timeout bounds the whole script run (helm install + kubectl wait can be slow).
	// Defaults to 6 minutes when zero.
	Timeout time.Duration
}

// repoScriptsDir resolves the absolute path of deployments/scripts relative to
// this source file, so the scripts can be located regardless of the working
// directory ginkgo runs from. This file lives at
// <repo>/test/e2e/operations/environment/scripts.go, so the repo root is four
// directories up.
func repoScriptsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "failed to resolve caller path for script location")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(repoRoot, "deployments", "scripts")
}

// runScript executes the named script with the script-specific env vars layered
// on top of the ambient environment. It returns an error rather than asserting,
// so callers can choose to fail hard (tests) or log-and-continue (cleanup).
func runScript(scriptName string, params *ScriptParams, extraEnv []string) error {
	timeout := params.Timeout
	if timeout == 0 {
		timeout = 6 * time.Minute
	}
	scriptPath := filepath.Join(repoScriptsDir(), scriptName)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	env := append([]string{}, extraEnv...)
	env = append(env,
		"ENV_NAME="+params.EnvName,
		"AGENT_MANAGER_TOKEN="+params.Token,
		"ORG_NAME="+params.OrgName,
		"AGENT_MANAGER_URL="+params.AMPBaseURL,
	)

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	// Inherit the ambient environment (PATH, KUBECONFIG, etc.) and layer the
	// script-specific variables on top.
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Stdout = ginkgo.GinkgoWriter
	cmd.Stderr = ginkgo.GinkgoWriter

	ginkgo.GinkgoWriter.Printf("Running %s for env %q...\n", scriptName, params.EnvName)
	return cmd.Run()
}

// envOr returns the ambient value of key when set (non-blank), else fallback.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// AddEnvironment runs deployments/scripts/add-environment.sh, which creates the
// environment via the Agent Manager API and installs its API Platform Gateway.
// Asserts the script succeeds.
func AddEnvironment(params *ScriptParams) {
	chartVersion := strings.TrimSpace(os.Getenv("CHART_VERSION"))
	Expect(chartVersion).NotTo(BeEmpty(),
		"CHART_VERSION must be set for add-environment.sh; set it to the chart version "+
			"the platform was installed with (see test/e2e/.env.example)")

	topology := params.Topology
	if topology == "" {
		topology = envOr("GATEWAY_TOPOLOGY", "single")
	}

	extra := []string{
		"DISPLAY_NAME=" + params.DisplayName,
		"CHART_VERSION=" + chartVersion,
		fmt.Sprintf("IS_PRODUCTION=%t", params.IsProduction),
		"GATEWAY_TOPOLOGY=" + topology,
		"AGENT_MANAGER_INTERNAL_BASE_URL=" + envOr("AGENT_MANAGER_INTERNAL_BASE_URL",
			"http://amp-api.wso2-amp.svc.cluster.local:9000"),
		"AGENT_MANAGER_INTERNAL_CP=" + envOr("AGENT_MANAGER_INTERNAL_CP",
			"amp-api-gateway-manager.wso2-amp.svc.cluster.local:9243"),
	}
	err := runScript("add-environment.sh", params, extra)
	Expect(err).NotTo(HaveOccurred(), "add-environment.sh failed for env %q", params.EnvName)
}

// RemoveEnvironment runs deployments/scripts/remove-environment.sh, which
// uninstalls the gateway helm release and deletes the environment. It is
// best-effort: it returns any error instead of asserting, so the stale-resource
// sweep can log-and-continue rather than abort.
func RemoveEnvironment(params *ScriptParams) error {
	return runScript("remove-environment.sh", params, nil)
}

// FromClient builds ScriptParams from the test client/config, filling in the
// org, base URL and bearer token. The caller sets EnvName/DisplayName.
func FromClient(client *framework.AMPClient) *ScriptParams {
	cfg := client.Cfg()
	return &ScriptParams{
		OrgName:    cfg.DefaultOrg,
		AMPBaseURL: cfg.AMPBaseURL,
		Token:      client.Token(),
	}
}
