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

	"github.com/wso2/agent-manager/agent-manager-service/instrumentation"
)

// The catalog is process-wide, so install it once for every case below. Each
// package gets its own test binary, so this does not leak to other packages.
func TestGetInstrumentationImage(t *testing.T) {
	const mirrorRepo = "mirror.test/amp-python-instrumentation-provider"
	instrumentation.SetCatalog(instrumentation.NewForTest(
		[]instrumentation.Version{
			{Version: "0.4.1", PythonVersions: []string{"3.11"}, ImageRepository: mirrorRepo},
		},
		"0.4.1",
	))

	t.Run("catalog entry redirects the repository", func(t *testing.T) {
		// The point of instrumentation.catalogExtension: an operator points a
		// version at an internal mirror and the init container follows.
		got, err := getInstrumentationImage("3.11", "0.4.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := mirrorRepo + ":0.4.1-python3.11"; got != want {
			t.Errorf("image = %q, want %q", got, want)
		}
	})

	t.Run("version absent from the catalog falls back to the public default", func(t *testing.T) {
		got, err := getInstrumentationImage("3.12", "9.9.9")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := DefaultInstrumentationImageRepository + ":9.9.9-python3.12"; got != want {
			t.Errorf("image = %q, want %q", got, want)
		}
	})

	t.Run("padded language version yields a clean tag", func(t *testing.T) {
		got, err := getInstrumentationImage("  3.11.9  ", "0.4.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := mirrorRepo + ":0.4.1-python3.11"; got != want {
			t.Errorf("image = %q, want %q", got, want)
		}
	})

	t.Run("language version without a minor is rejected", func(t *testing.T) {
		if _, err := getInstrumentationImage("3", "0.4.1"); err == nil {
			t.Error("expected an error for a language version with no minor")
		}
	})
}
