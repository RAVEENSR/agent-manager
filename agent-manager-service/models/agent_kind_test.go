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

package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func strPtr(s string) *string { return &s }

func TestRedactSecretDefaults(t *testing.T) {
	t.Run("replaces a secret item's real default with the placeholder, leaves non-secret items untouched", func(t *testing.T) {
		schema := []KindConfigSchemaItem{
			{Name: "OPENAI_API_KEY", IsSecret: true, IsMandatory: true, DefaultValue: strPtr("sk-real-secret")},
			{Name: "MODEL_NAME", IsSecret: false, IsMandatory: false, DefaultValue: strPtr("gpt-4")},
		}

		redacted := RedactSecretDefaults(schema)

		require := assert.New(t)
		if require.NotNil(redacted[0].DefaultValue, "a secret item WITH a default must signal that a default exists") {
			require.Equal(RedactedSecretDefaultPlaceholder, *redacted[0].DefaultValue)
		}
		require.NotNil(redacted[1].DefaultValue)
		require.Equal("gpt-4", *redacted[1].DefaultValue, "non-secret default must pass through unchanged")
	})

	t.Run("does not mutate the source slice", func(t *testing.T) {
		schema := []KindConfigSchemaItem{
			{Name: "API_KEY", IsSecret: true, DefaultValue: strPtr("sk-real-secret")},
		}

		_ = RedactSecretDefaults(schema)

		if assert.NotNil(t, schema[0].DefaultValue, "redaction must not mutate the caller's slice") {
			assert.Equal(t, "sk-real-secret", *schema[0].DefaultValue)
		}
	})

	t.Run("secret item with no default stays nil, distinguishing it from a hidden default", func(t *testing.T) {
		schema := []KindConfigSchemaItem{
			{Name: "OPTIONAL_TOKEN", IsSecret: true, DefaultValue: nil},
		}

		redacted := RedactSecretDefaults(schema)

		assert.Nil(t, redacted[0].DefaultValue, "no default set — this must stay absent, not become the placeholder")
	})

	t.Run("secret item with an empty-string default is treated the same as no default", func(t *testing.T) {
		empty := ""
		schema := []KindConfigSchemaItem{
			{Name: "OPTIONAL_TOKEN", IsSecret: true, DefaultValue: &empty},
		}

		redacted := RedactSecretDefaults(schema)

		assert.Nil(t, redacted[0].DefaultValue)
	})

	t.Run("nil schema returns an empty, non-nil slice", func(t *testing.T) {
		redacted := RedactSecretDefaults(nil)
		assert.NotNil(t, redacted)
		assert.Empty(t, redacted)
	})
}
