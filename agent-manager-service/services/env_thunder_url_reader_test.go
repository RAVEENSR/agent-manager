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

package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// TestResolveThunderHandle covers the SINGLE centralized function every caller
// (EnvironmentService's readThunderHandle/GetThunderURL/SetThunderURL, and the
// resolver's ReadThunderHandleFunc via NewEnvThunderURLReader) delegates to.
func TestResolveThunderHandle(t *testing.T) {
	t.Run("returns the registered handle", func(t *testing.T) {
		urlRepo := &repomocks.EnvThunderURLRepositoryMock{
			GetFunc: func(_ context.Context, ouID, envName string) (*models.EnvThunderURL, error) {
				assert.Equal(t, "ou-1", ouID)
				assert.Equal(t, "prod", envName)
				return &models.EnvThunderURL{ThunderHandle: "x7f2q9kz"}, nil
			},
		}

		handle, err := ResolveThunderHandle(context.Background(), urlRepo, "ou-1", "prod")
		require.NoError(t, err)
		assert.Equal(t, "x7f2q9kz", handle)
	})

	t.Run("reports not-provisioned when no row exists — never recomputes a value", func(t *testing.T) {
		urlRepo := &repomocks.EnvThunderURLRepositoryMock{
			GetFunc: func(context.Context, string, string) (*models.EnvThunderURL, error) {
				return nil, gorm.ErrRecordNotFound
			},
		}

		handle, err := ResolveThunderHandle(context.Background(), urlRepo, "ou-1", "prod")
		require.NoError(t, err)
		assert.Empty(t, handle)
	})

	t.Run("propagates an unexpected repo error", func(t *testing.T) {
		boom := errors.New("db down")
		urlRepo := &repomocks.EnvThunderURLRepositoryMock{
			GetFunc: func(context.Context, string, string) (*models.EnvThunderURL, error) {
				return nil, boom
			},
		}

		_, err := ResolveThunderHandle(context.Background(), urlRepo, "ou-1", "prod")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
	})
}
