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

// externalSecretNode builds an ExternalSecret node whose own status reports the given Ready
// condition. Its health field is deliberately Healthy: that is what OpenChoreo reports even
// for a secret that is failing to sync, which is why the object status is what gets read.
func externalSecretNode(name, condStatus, reason, message string) gen.ResourceNode {
	healthy := gen.HealthInfo{}
	return gen.ResourceNode{
		Kind:   "ExternalSecret",
		Name:   name,
		Uid:    "es-" + name,
		Health: &healthy,
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type": "Ready", "status": condStatus, "reason": reason, "message": message,
					},
				},
			},
		},
	}
}

// treeWith builds a tree from a warm pool status plus any extra nodes.
func treeWith(poolStatus map[string]interface{}, extra ...gen.ResourceNode) *gen.K8sResourceTreeResponse {
	tree := warmPoolTree(poolStatus)
	tree.RenderedReleases[0].Nodes = append(tree.RenderedReleases[0].Nodes, extra...)
	return tree
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

// A resource reporting itself not ready, with nothing serving, must report failed rather
// than sitting at in-progress forever. Reproduces the observed case: an ExternalSecret that
// will not sync leaves the container in CreateContainerConfigError ("secret ... not found")
// indefinitely.
func TestNotReadyResourceIsReportedAsFailed(t *testing.T) {
	const syncErr = "could not get secret data from provider"

	t.Run("unsyncable secret with nothing ready is failed", func(t *testing.T) {
		tree := treeWith(
			map[string]interface{}{"replicas": float64(1)},
			externalSecretNode("env-secrets", "False", "SecretSyncedError", syncErr),
		)
		state := runtimeReplicaStateFromTree(tree)

		assert.True(t, state.isFailed())
		assert.False(t, state.isBooting(), "a pod that cannot start is not starting")
		assert.Contains(t, state.notReadyResource, "SecretSyncedError")
		assert.Equal(t, DeploymentStatusFailed, determineDeploymentStatus(readyBinding(), state))
	})

	t.Run("unsyncable secret while a replica still serves stays active", func(t *testing.T) {
		// Observed live: the ExternalSecret was failing for ~30 minutes while the pod ran on
		// a previously synced Secret. The agent was serving, so it is not failed.
		tree := treeWith(
			map[string]interface{}{"replicas": float64(1), "readyReplicas": float64(1)},
			externalSecretNode("env-secrets", "False", "SecretSyncedError", syncErr),
		)
		state := runtimeReplicaStateFromTree(tree)

		assert.False(t, state.isFailed())
		assert.Equal(t, DeploymentStatusActive, determineDeploymentStatus(readyBinding(), state))
	})

	t.Run("synced secrets with nothing ready is still in progress", func(t *testing.T) {
		tree := treeWith(
			map[string]interface{}{"replicas": float64(1)},
			externalSecretNode("env-secrets", "True", "SecretSynced", "secret synced"),
		)
		state := runtimeReplicaStateFromTree(tree)

		assert.Empty(t, state.notReadyResource)
		assert.Equal(t, DeploymentStatusInProgress, determineDeploymentStatus(readyBinding(), state))
	})

	t.Run("secret with no conditions is not treated as a failure", func(t *testing.T) {
		node := gen.ResourceNode{
			Kind: "ExternalSecret", Name: "fresh", Uid: "u",
			Object: map[string]interface{}{},
		}
		state := runtimeReplicaStateFromTree(treeWith(map[string]interface{}{"replicas": float64(1)}, node))

		assert.Empty(t, state.notReadyResource)
		assert.True(t, state.isBooting())
	})

	t.Run("any kind reporting Ready=False counts, not just ExternalSecret", func(t *testing.T) {
		// No kind is special-cased. ExternalSecret is merely the only kind in the tree today
		// that publishes a Ready condition; Backend and RestApi use Accepted/Programmed and
		// so are ignored whatever they report.
		blocked := externalSecretNode("x", "False", "Blocked", "nope")
		blocked.Kind = "SomeFutureKind"
		accepted := externalSecretNode("gw", "False", "NotAccepted", "ignored")
		accepted.Kind = "Backend"
		accepted.Object["status"].(map[string]interface{})["conditions"] = []interface{}{
			map[string]interface{}{"type": "Accepted", "status": "False", "reason": "NotAccepted"},
		}

		state := runtimeReplicaStateFromTree(
			treeWith(map[string]interface{}{"replicas": float64(1)}, accepted, blocked))

		assert.Contains(t, state.notReadyResource, "SomeFutureKind")
		assert.NotContains(t, state.notReadyResource, "Backend", "a non-Ready condition type is ignored")
	})

	t.Run("scaled to zero with a failing secret is not failed", func(t *testing.T) {
		tree := treeWith(
			map[string]interface{}{"replicas": float64(0), "readyReplicas": float64(0)},
			externalSecretNode("env-secrets", "False", "SecretSyncedError", syncErr),
		)
		assert.False(t, runtimeReplicaStateFromTree(tree).isFailed())
	})
}
