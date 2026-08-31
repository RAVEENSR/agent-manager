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

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/gen"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// A Component must record kind=Workflow on its workflow reference. A
// ClusterWorkflow pins workflowPlaneRef to the single cluster-wide
// ClusterWorkflowPlane, so its builds always run on that one plane; a namespaced
// Workflow resolves the WorkflowPlane inside the org's own namespace, which is
// what lets the org build on the data centre it belongs to.
//
// The kind has to be recorded here, at creation, because builds.go reads it from
// the Component and only falls back to ClusterWorkflow when it is absent.
func TestBuildComponentRequest_RecordsNamespacedWorkflowKind(t *testing.T) {
	for _, tc := range []struct {
		name         string
		build        *BuildConfig
		wantWorkflow string
	}{
		{
			name:         "docker build",
			build:        &BuildConfig{Type: BuildTypeDocker, Docker: &DockerConfig{DockerfilePath: "Dockerfile"}},
			wantWorkflow: WorkflowNameDocker,
		},
		{
			name:         "google buildpack",
			build:        &BuildConfig{Type: BuildTypeBuildpack, Buildpack: &BuildpackConfig{Language: "python"}},
			wantWorkflow: WorkflowNameGoogleCloudBuildpacks,
		},
		{
			name:         "ballerina buildpack",
			build:        &BuildConfig{Type: BuildTypeBuildpack, Buildpack: &BuildpackConfig{Language: "ballerina"}},
			wantWorkflow: WorkflowNameBallerinaBuilpack,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := buildInternalAgentFromSourceComponentRequestBody("default", "proj1", CreateComponentRequest{
				Name:             "my-agent",
				DisplayName:      "My Agent",
				ProvisioningType: ProvisioningInternal,
				AgentType:        AgentTypeConfig{Type: string(utils.AgentTypeAPI), SubType: "custom-api"},
				Build:            tc.build,
				Repository:       &RepositoryConfig{},
			})
			require.NoError(t, err)
			require.NotNil(t, body.Spec)
			require.NotNil(t, body.Spec.Workflow)
			require.NotNil(t, body.Spec.Workflow.Kind,
				"kind must be recorded, otherwise builds.go falls back to ClusterWorkflow")
			assert.Equal(t, gen.ComponentWorkflowConfigKindWorkflow, *body.Spec.Workflow.Kind,
				"must be the namespaced Workflow, not the cluster-scoped ClusterWorkflow")
			assert.Equal(t, tc.wantWorkflow, body.Spec.Workflow.Name,
				"the workflow name is unchanged by the kind switch — the namespaced "+
					"workflows are provisioned under the same names")
		})
	}
}
