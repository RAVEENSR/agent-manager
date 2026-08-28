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

package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/spec"
)

func TestRedactSecretConfigValues(t *testing.T) {
	t.Run("nil configurations stay nil", func(t *testing.T) {
		require.Nil(t, RedactSecretConfigValues(nil))
	})

	t.Run("sensitive values are omitted and plain ones survive", func(t *testing.T) {
		cfg := &spec.Configurations{
			Env: []spec.EnvironmentVariable{
				{Key: "OPENAI_API_KEY", Value: spec.PtrString("sk-super-secret"), IsSensitive: spec.PtrBool(true)},
				{Key: "LOG_LEVEL", Value: spec.PtrString("debug")},
			},
			Files: []spec.FileMount{
				{Key: "creds.json", MountPath: "/etc/creds.json", Value: spec.PtrString("file-super-secret"), IsSensitive: spec.PtrBool(true)},
				{Key: "app.conf", MountPath: "/etc/app.conf", Value: spec.PtrString("plain=1")},
			},
		}

		redacted := RedactSecretConfigValues(cfg)

		require.Nil(t, redacted.Env[0].Value)
		require.Equal(t, "debug", *redacted.Env[1].Value)
		require.Nil(t, redacted.Files[0].Value)
		require.Equal(t, "plain=1", *redacted.Files[1].Value)

		// A nil *string with json:"value,omitempty" must drop the key entirely,
		// not serialize as an empty string.
		body, err := json.Marshal(redacted)
		require.NoError(t, err)
		require.NotContains(t, string(body), "sk-super-secret")
		require.NotContains(t, string(body), "file-super-secret")
		require.Contains(t, string(body), "debug")
	})

	t.Run("the caller's configurations are left intact", func(t *testing.T) {
		// The handler redacts a pointer into the decoded request body, which the
		// service still holds; redacting in place would corrupt it.
		cfg := &spec.Configurations{
			Env: []spec.EnvironmentVariable{
				{Key: "OPENAI_API_KEY", Value: spec.PtrString("sk-super-secret"), IsSensitive: spec.PtrBool(true)},
			},
		}

		RedactSecretConfigValues(cfg)

		require.NotNil(t, cfg.Env[0].Value)
		require.Equal(t, "sk-super-secret", *cfg.Env[0].Value)
	})

	t.Run("unrelated configuration fields are carried through", func(t *testing.T) {
		cfg := &spec.Configurations{
			EnableAutoInstrumentation: spec.PtrBool(true),
			EnableApiKeySecurity:      spec.PtrBool(false),
			ResilienceTimeoutSeconds:  spec.PtrInt32(45),
		}

		redacted := RedactSecretConfigValues(cfg)

		require.True(t, *redacted.EnableAutoInstrumentation)
		require.False(t, *redacted.EnableApiKeySecurity)
		require.Equal(t, int32(45), *redacted.ResilienceTimeoutSeconds)
	})
}
