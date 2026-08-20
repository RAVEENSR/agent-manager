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
//

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/gen"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// newTestClient wires an openChoreoClient against a stub OpenChoreo API.
func newTestClient(t *testing.T, handler http.Handler) *openChoreoClient {
	t.Helper()
	srv := httptest.NewServer(jsonContentType(handler))
	t.Cleanup(srv.Close)

	gc, err := gen.NewClientWithResponses(srv.URL)
	require.NoError(t, err)
	return &openChoreoClient{ocClient: gc, defaultNamespace: "default"}
}

// jsonContentType stamps the header the generated response parsers key on to
// decide whether a body is decodable — without it every stub reply parses as an
// empty struct.
func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func TestProjectReleaseBindingName(t *testing.T) {
	// Must stay in step with the Helm chart's
	// templates/project-release-binding.yaml, or the chart-managed binding for
	// the default project would be duplicated under a second name.
	assert.Equal(t, "my-project-dev", projectReleaseBindingName("my-project", "dev"))
}

func TestEnsureProjectReleaseBinding_CreatesBinding(t *testing.T) {
	var gotBody gen.ProjectReleaseBinding
	var gotPath string
	srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(gotBody))
	}))

	err := srv.EnsureProjectReleaseBinding(context.Background(), "acme", "my-project", "dev")

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/namespaces/default/projectreleasebindings", gotPath)
	assert.Equal(t, "my-project-dev", gotBody.Metadata.Name)
	require.NotNil(t, gotBody.Spec)
	assert.Equal(t, "my-project", gotBody.Spec.Owner.ProjectName)
	assert.Equal(t, "dev", gotBody.Spec.Environment)
	// The Project controller seeds the pin with the latest ProjectRelease;
	// sending one here would freeze the project at whatever release existed now.
	assert.Nil(t, gotBody.Spec.ProjectRelease)
}

func TestEnsureProjectReleaseBinding_ExistingBindingIsSuccess(t *testing.T) {
	srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			require.NoError(t, json.NewEncoder(w).Encode(gen.Conflict{Error: "already exists"}))
			return
		}
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(gen.ProjectReleaseBinding{
			Metadata: gen.ObjectMeta{Name: "my-project-dev"},
			Spec: &gen.ProjectReleaseBindingSpec{
				Owner: struct {
					ProjectName string `json:"projectName"`
				}{ProjectName: "my-project"},
				Environment: "dev",
			},
		}))
	}))

	err := srv.EnsureProjectReleaseBinding(context.Background(), "acme", "my-project", "dev")

	require.NoError(t, err, "an existing binding for the same project and environment is what we wanted")
}

func TestEnsureProjectReleaseBinding_NameCollisionWithAnotherProjectIsAnError(t *testing.T) {
	// Binding names are "<project>-<environment>", so project "my" + env
	// "project-dev" collides with project "my-project" + env "dev". Treating
	// that as success would silently point one project at another's namespace.
	srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			require.NoError(t, json.NewEncoder(w).Encode(gen.Conflict{Error: "already exists"}))
			return
		}
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(gen.ProjectReleaseBinding{
			Metadata: gen.ObjectMeta{Name: "my-project-dev"},
			Spec: &gen.ProjectReleaseBindingSpec{
				Owner: struct {
					ProjectName string `json:"projectName"`
				}{ProjectName: "my"},
				Environment: "project-dev",
			},
		}))
	}))

	err := srv.EnsureProjectReleaseBinding(context.Background(), "acme", "my-project", "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `already exists for project "my" environment "project-dev"`)
}

func TestEnsureProjectReleaseBinding_PropagatesAPIErrors(t *testing.T) {
	srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		require.NoError(t, json.NewEncoder(w).Encode(gen.Forbidden{Error: "no permission"}))
	}))

	err := srv.EnsureProjectReleaseBinding(context.Background(), "acme", "my-project", "dev")

	assert.ErrorIs(t, err, utils.ErrForbidden)
}
