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

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// InvokeSecurityProbe calls one fixed probe operation through the agent's
// public API-key-protected endpoint. The raw body is checked for credential-
// shaped fields before decoding, so a fixture regression cannot print a token
// into Ginkgo/JUnit output through an assertion failure.
func InvokeSecurityProbe[T any](ctx context.Context, g Gomega, method, endpointURL, apiKey string) T {
	client := &http.Client{Timeout: 45 * time.Second}
	var decoded T

	g.Eventually(func(attempt Gomega) {
		request, err := http.NewRequestWithContext(ctx, method, endpointURL, bytes.NewReader(nil))
		attempt.Expect(err).NotTo(HaveOccurred(), "create security probe request")
		request.Header.Set("X-API-Key", apiKey)
		request.Header.Set("Content-Type", "application/json")

		response, err := client.Do(request)
		attempt.Expect(err).NotTo(HaveOccurred(), "security probe endpoint not reachable")
		defer response.Body.Close()

		body, err := io.ReadAll(response.Body)
		attempt.Expect(err).NotTo(HaveOccurred(), "read security probe response")
		if response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden ||
			response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusGatewayTimeout {
			attempt.Expect(response.StatusCode).To(Equal(http.StatusOK),
				"agent gateway is not ready (status %d)", response.StatusCode)
			return
		}
		if response.StatusCode != http.StatusOK {
			StopTrying(fmt.Sprintf("security probe returned status %d", response.StatusCode)).Now()
		}

		normalized := strings.ToLower(strings.ReplaceAll(string(body), "_", ""))
		attempt.Expect(normalized).NotTo(ContainSubstring("accesstoken"), "probe response exposed an access-token field")
		attempt.Expect(normalized).NotTo(ContainSubstring("clientsecret"), "probe response exposed a client-secret field")
		attempt.Expect(normalized).NotTo(ContainSubstring("authorization"), "probe response exposed authorization data")
		attempt.Expect(json.Unmarshal(body, &decoded)).To(Succeed(), "decode security probe response")
	}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

	ginkgo.GinkgoWriter.Printf("Security probe completed: %s %s\n", method, endpointURL)
	return decoded
}
