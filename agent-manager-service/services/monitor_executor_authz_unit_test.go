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

// UNIT test guarding the authorization contract of ExecuteMonitorRun.
//
// When the scheduler runs a monitor it uses an OpenChoreo client bound to the
// amp-monitor-scheduler ClusterAuthzRole, which is deliberately minimal. Because
// ocClientFromContext hands that restricted client to ExecuteMonitorRun, ANY
// OpenChoreo call added to the execution path silently inherits it — and
// OpenChoreo list endpoints filter by permission and return an empty list rather
// than a 403, so a missing action surfaces far downstream as a confusing
// "not found" instead of an authorization error. That is exactly how the
// scheduler's namespace lookup shipped broken once: adding the ResolveNamespace
// call (which lists organizations) without granting namespace:view left every
// scheduled monitor failing with "no organization found for namespace resolution".
//
// This test closes that loop. It drives ExecuteMonitorRun with a mocked
// OpenChoreo client, discovers which client methods were actually invoked, and
// asserts each one's required action is granted by the chart. Adding a new
// OpenChoreo call to the execution path fails this test until the action is
// added to BOTH the mapping below and the ClusterAuthzRole in the chart.
package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// schedulerRolePath locates the chart template that defines the scheduler's
// ClusterAuthzRole, relative to this source file so the test is independent of
// the working directory the suite runs from.
const schedulerRolePath = "deployments/helm-charts/wso2-amp-platform-resources-extension/templates/authz-cluster-role.yaml"

// ocMethodRequiredAction maps each OpenChoreo client method reachable from
// ExecuteMonitorRun to the authz action it requires. The mapping is not
// mechanical — ResolveNamespace reaches OpenChoreo through ListOrganizations,
// so listing organizations is what namespace:view actually gates. Any method
// invoked but absent here fails the test rather than being assumed harmless.
var ocMethodRequiredAction = map[string]string{
	"ListOrganizations": "namespace:view",
	"CreateWorkflowRun": "workflowrun:create",
	"GetWorkflowRun":    "workflowrun:view",
}

// grantedSchedulerActions parses the actions granted to amp-monitor-scheduler out
// of the chart template. The file is a Helm template, so it is read line-wise
// rather than through a YAML parser (the label block contains {{ }} actions).
func grantedSchedulerActions(t *testing.T) map[string]bool {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "could not determine test source location")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	raw, err := os.ReadFile(filepath.Join(repoRoot, schedulerRolePath))
	require.NoError(t, err, "could not read the scheduler ClusterAuthzRole chart template")

	granted := map[string]bool{}
	inActions := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "actions:" {
			inActions = true
			continue
		}
		if !inActions {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break // end of the actions block
		}
		granted[strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))] = true
	}

	require.NotEmpty(t, granted, "parsed no actions from %s — has the chart layout changed?", schedulerRolePath)
	return granted
}

// toString renders a recovered panic value for message matching.
func toString(v interface{}) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	return fmt.Sprintf("%v", v)
}

// invokedOCMethods reports which methods of the mock were called, by invoking
// every moq-generated "<Method>Calls()" accessor and keeping the non-empty ones.
// Reflection is what makes this guard durable: a newly added OpenChoreo call is
// detected without anyone remembering to update an assertion.
func invokedOCMethods(mock *clientmocks.OpenChoreoClientMock) []string {
	var invoked []string
	v := reflect.ValueOf(mock)
	for i := 0; i < v.NumMethod(); i++ {
		name := v.Type().Method(i).Name
		if !strings.HasSuffix(name, "Calls") {
			continue
		}
		m := v.Method(i)
		if m.Type().NumIn() != 0 || m.Type().NumOut() != 1 {
			continue
		}
		if out := m.Call(nil); out[0].Kind() == reflect.Slice && out[0].Len() > 0 {
			invoked = append(invoked, strings.TrimSuffix(name, "Calls"))
		}
	}
	return invoked
}

func TestExecuteMonitorRunStaysWithinSchedulerGrantedActions(t *testing.T) {
	monitorID := uuid.New()
	monitor := &models.Monitor{
		ID:              monitorID,
		Name:            "monitor-scheduled",
		OUID:            "ou-1",
		ProjectName:     "proj",
		AgentName:       "agent",
		EnvironmentName: "dev",
		Evaluators: []models.MonitorEvaluator{
			{Identifier: "latency_performance", DisplayName: "Latency", Config: map[string]interface{}{"level": "trace", "max_latency_ms": float64(3000)}},
		},
	}

	ocMock := &clientmocks.OpenChoreoClientMock{
		ListOrganizationsFunc: func(ctx context.Context) ([]*models.OrganizationResponse, error) {
			return []*models.OrganizationResponse{{Name: "ou-1", Namespace: "ou-1-ns"}}, nil
		},
		CreateWorkflowRunFunc: func(ctx context.Context, ouID string, req client.CreateWorkflowRunRequest) (*client.WorkflowRunResponse, error) {
			return &client.WorkflowRunResponse{Name: "monitor-scheduled-abc123"}, nil
		},
	}

	monitorRepo := &repomocks.MonitorRepositoryMock{
		CreateMonitorRunFunc: func(run *models.MonitorRun) error { return nil },
	}
	// buildPublishingParams looks up per-org publisher credentials and falls back to
	// static defaults when none exist, which is the on-prem single-tenant path.
	credRepo := &repomocks.OrgPublisherCredentialRepositoryMock{
		GetByOrgNameFunc: func(ouID string) (*models.OrgPublisherCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	// No LLM proxy mapping: resolveLLMProxyConfig short-circuits, so the gateway and
	// provider repositories are never reached and stay unwired.
	llmMappingRepo := &repomocks.MonitorLLMMappingRepositoryMock{
		ListByMonitorIDFunc: func(ctx context.Context, id uuid.UUID) ([]models.MonitorLLMMapping, error) {
			return []models.MonitorLLMMapping{}, nil
		},
	}

	executor := NewMonitorExecutor(
		ocMock,
		discardLogger(),
		monitorRepo,
		&repomocks.CustomEvaluatorRepositoryMock{},
		credRepo,
		llmMappingRepo,
		&repomocks.GatewayRepositoryMock{},
		&repomocks.LLMProviderRepositoryMock{},
		&repomocks.DeploymentRepositoryMock{},
	)

	// The scheduler injects its restricted, role-bound client through the context;
	// go through that same path so the test exercises the real wiring.
	ctx := withOCClient(context.Background(), client.OpenChoreoClient(ocMock))

	// An unwired collaborator method panics inside moq. When it is an OpenChoreo
	// call, that is precisely the regression this test exists to catch, so give a
	// directed message; anything else is just an unwired fixture.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if strings.Contains(strings.ToLower(toString(r)), "openchoreoclientmock") {
			t.Fatalf("ExecuteMonitorRun calls an OpenChoreo method this test does not wire: %v\n\n"+
				"That call inherits the restricted amp-monitor-scheduler role. Work out which action it\n"+
				"needs, grant it in %s, and add the method to ocMethodRequiredAction.", r, schedulerRolePath)
		}
		t.Fatalf("unwired collaborator in this test's fixture (not an authz problem): %v", r)
	}()

	_, err := executor.ExecuteMonitorRun(ctx, ExecuteMonitorRunParams{
		OUID:       "ou-1",
		Monitor:    monitor,
		StartTime:  time.Now().Add(-5 * time.Minute),
		EndTime:    time.Now(),
		Evaluators: monitor.Evaluators,
	})
	require.NoError(t, err)

	invoked := invokedOCMethods(ocMock)
	require.NotEmpty(t, invoked, "no OpenChoreo calls recorded — the guard would pass vacuously")
	assert.Contains(t, invoked, "ListOrganizations",
		"ResolveNamespace must reach OpenChoreo via ListOrganizations; if this changed, revisit which action gates it")

	granted := grantedSchedulerActions(t)
	for _, method := range invoked {
		action, mapped := ocMethodRequiredAction[method]
		if !mapped {
			t.Errorf("ExecuteMonitorRun calls OpenChoreo method %q, which has no entry in ocMethodRequiredAction.\n"+
				"Determine the authz action it needs, add it there, and grant it in %s.", method, schedulerRolePath)
			continue
		}
		assert.Truef(t, granted[action],
			"ExecuteMonitorRun calls %q which requires action %q, but amp-monitor-scheduler does not grant it in %s.\n"+
				"Scheduled monitors will fail at runtime — and because OpenChoreo list endpoints return an empty\n"+
				"list instead of 403, the symptom will be a misleading downstream error.", method, action, schedulerRolePath)
	}
}

// TestSchedulerRoleGrantsNamespaceView pins the specific grant that scheduled
// monitors regressed on, so removing it fails loudly and by name.
func TestSchedulerRoleGrantsNamespaceView(t *testing.T) {
	granted := grantedSchedulerActions(t)
	assert.True(t, granted["namespace:view"],
		"amp-monitor-scheduler must grant namespace:view — ExecuteMonitorRun resolves the org namespace "+
			"through ListOrganizations, and without it every scheduled monitor fails with "+
			"\"no organization found for namespace resolution\"")
}
