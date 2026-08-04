//
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

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/gen"
)

// warmPoolTree builds a resource tree containing a SandboxWarmPool node with the given
// status, mirroring the shape returned by the OpenChoreo k8sresources/tree endpoint.
func warmPoolTree(status map[string]interface{}) *gen.K8sResourceTreeResponse {
	pool := gen.ResourceNode{
		Kind: resourceKindSandboxWarmPool,
		Name: "it-helpdesk-agent",
		Uid:  "pool-uid",
		Object: map[string]interface{}{
			"kind": resourceKindSandboxWarmPool,
		},
	}
	if status != nil {
		pool.Object["status"] = status
	}

	return &gen.K8sResourceTreeResponse{
		RenderedReleases: []gen.ReleaseResourceTree{{
			Name: "it-helpdesk-agent-default",
			Nodes: []gen.ResourceNode{
				{Kind: "Service", Name: "it-helpdesk-agent", Uid: "svc-uid", Object: map[string]interface{}{}},
				pool,
			},
		}},
	}
}

func readyBinding() *gen.ReleaseBinding {
	return &gen.ReleaseBinding{
		Metadata: gen.ObjectMeta{Name: "it-helpdesk-agent-default"},
		Spec:     &gen.ReleaseBindingSpec{Environment: "default"},
		Status: &gen.ReleaseBindingStatus{
			Conditions: &[]gen.Condition{{Type: "Ready", Status: "True", Reason: "Ready"}},
		},
	}
}

func TestRuntimeReplicaStateFromTree(t *testing.T) {
	t.Run("nil tree is unknown", func(t *testing.T) {
		assert.Equal(t, runtimeReplicaState{}, runtimeReplicaStateFromTree(nil))
	})

	t.Run("no warm pool node is unknown", func(t *testing.T) {
		tree := &gen.K8sResourceTreeResponse{
			RenderedReleases: []gen.ReleaseResourceTree{{
				Name:  "r",
				Nodes: []gen.ResourceNode{{Kind: "Service", Name: "svc", Uid: "u", Object: map[string]interface{}{}}},
			}},
		}
		assert.False(t, runtimeReplicaStateFromTree(tree).found)
	})

	t.Run("all replicas ready", func(t *testing.T) {
		// JSON numbers decode as float64, which is how the counts actually arrive.
		got := runtimeReplicaStateFromTree(warmPoolTree(map[string]interface{}{
			"replicas": float64(1), "readyReplicas": float64(1),
		}))
		assert.Equal(t, runtimeReplicaState{found: true, desired: 1, ready: 1}, got)
		assert.False(t, got.isBooting())
	})

	t.Run("no replica ready yet", func(t *testing.T) {
		// The warm pool omits readyReplicas entirely while the pod boots.
		got := runtimeReplicaStateFromTree(warmPoolTree(map[string]interface{}{"replicas": float64(1)}))
		assert.Equal(t, runtimeReplicaState{found: true, desired: 1, ready: 0}, got)
		assert.True(t, got.isBooting())
	})

	t.Run("pool without status is not ready", func(t *testing.T) {
		got := runtimeReplicaStateFromTree(warmPoolTree(nil))
		assert.True(t, got.found)
		assert.False(t, got.isBooting(), "desired is unknown, so nothing is claimed about readiness")
	})

	t.Run("scaled to zero is not booting", func(t *testing.T) {
		got := runtimeReplicaStateFromTree(warmPoolTree(map[string]interface{}{
			"replicas": float64(0), "readyReplicas": float64(0),
		}))
		assert.False(t, got.isBooting(), "an agent scaled to zero is not mid-startup")
	})

	t.Run("partially ready pool counts as ready", func(t *testing.T) {
		got := runtimeReplicaStateFromTree(warmPoolTree(map[string]interface{}{
			"replicas": float64(3), "readyReplicas": float64(1),
		}))
		assert.False(t, got.isBooting(), "one ready replica can already serve traffic")
	})
}

func TestDetermineDeploymentStatusWithRuntimeReadiness(t *testing.T) {
	t.Run("ready binding with no ready replica is in progress", func(t *testing.T) {
		booting := runtimeReplicaState{found: true, desired: 1, ready: 0}
		assert.Equal(t, DeploymentStatusInProgress, determineDeploymentStatus(readyBinding(), booting))
	})

	t.Run("ready binding with a ready replica is active", func(t *testing.T) {
		serving := runtimeReplicaState{found: true, desired: 1, ready: 1}
		assert.Equal(t, DeploymentStatusActive, determineDeploymentStatus(readyBinding(), serving))
	})

	t.Run("unknown readiness falls back to the binding", func(t *testing.T) {
		assert.Equal(t, DeploymentStatusActive, determineDeploymentStatus(readyBinding(), runtimeReplicaState{}))
	})

	t.Run("readiness does not override suspended", func(t *testing.T) {
		binding := readyBinding()
		state := gen.ReleaseBindingSpecStateUndeploy
		binding.Spec.State = &state
		booting := runtimeReplicaState{found: true, desired: 1, ready: 0}
		assert.Equal(t, DeploymentStatusSuspended, determineDeploymentStatus(binding, booting))
	})

	t.Run("readiness does not override failed", func(t *testing.T) {
		binding := readyBinding()
		binding.Status.Conditions = &[]gen.Condition{{Type: "Ready", Status: "False", Reason: "Failed"}}
		booting := runtimeReplicaState{found: true, desired: 1, ready: 0}
		assert.Equal(t, DeploymentStatusFailed, determineDeploymentStatus(binding, booting))
	})

	t.Run("nil binding is not deployed", func(t *testing.T) {
		assert.Equal(t, DeploymentStatusNotDeployed, determineDeploymentStatus(nil, runtimeReplicaState{}))
	})
}
