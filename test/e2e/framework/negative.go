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

package framework

import (
	"fmt"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Assertions for negative-path (security) specs.
//
// These differ from the ExpectStatus family in what they treat as the
// interesting signal: a denial must be a denial, and an allow must not be
// mistaken for one. A security spec that passes because the resource happened
// not to exist is worse than no spec at all, so the "allowed" assertion here
// is deliberately weak (anything but 403) while the "denied" assertion is
// exact.

// ExpectForbidden asserts the response is 403. Use for "this principal must
// not be able to do this" — the assertion is exact because 404 or 400 would
// mean the request reached the handler, i.e. authorization did not stop it.
func ExpectForbidden(g Gomega, resp *http.Response, context string) {
	if resp.StatusCode == http.StatusForbidden {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	g.Expect(resp.StatusCode).To(Equal(http.StatusForbidden),
		"%s: expected the request to be denied with 403, got %d. "+
			"A non-403 here means the request passed authorization and reached the handler. Body: %s",
		context, resp.StatusCode, string(body))
}

// ExpectNotForbidden asserts the request got past both authentication and
// authorization. This is the positive control for a scope-matrix spec: it
// proves the route is reachable with the right scope, so a paired
// ExpectForbidden is really measuring the scope check and not some unrelated
// rejection.
//
// It rejects 403 (authorization refused) and 401 (never authenticated) — a
// control that accepted 401 would pass even if the scoped token were invalid,
// which would make the whole pairing worthless. It deliberately does NOT
// require success: these specs drive non-existent resources on purpose, so
// 400/404/409 are all expected.
//
// A 5xx satisfies this narrowly scoped authorization control because the
// request passed the static scope middleware, but it is emitted as report
// evidence so handler-quality suites can track it. Dynamic-authorization routes
// need a stronger route-specific assertion because their resolver can itself 5xx.
func ExpectNotForbidden(g Gomega, resp *http.Response, context string) {
	switch resp.StatusCode {
	case http.StatusForbidden:
		body, _ := io.ReadAll(resp.Body)
		g.Expect(resp.StatusCode).NotTo(Equal(http.StatusForbidden),
			"%s: the request was denied with 403 even though the token carries the required scope. "+
				"Either the route is guarded by a different permission than expected, or an "+
				"additional guard (org match, audience) rejected it. Body: %s",
			context, string(body))
	case http.StatusUnauthorized:
		body, _ := io.ReadAll(resp.Body)
		g.Expect(resp.StatusCode).NotTo(Equal(http.StatusUnauthorized),
			"%s: the scoped token was rejected at authentication (401), so this spec never "+
				"exercised the authorization check it exists to control for. The token is "+
				"malformed, expired, or has the wrong audience. Body: %s",
			context, string(body))
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			message := fmt.Sprintf("%s returned %d after authorization passed", context, resp.StatusCode)
			AddReportEntry("post-authorization server error", message)
			fmt.Fprintf(GinkgoWriter, "POST-AUTHORIZATION 5xx: %s\n", message)
		}
	}
}

// ExpectUnauthorized asserts the response is 401. Use for missing, malformed,
// expired, or badly-signed tokens — anything the authentication layer should
// reject before authorization is even considered.
func ExpectUnauthorized(g Gomega, resp *http.Response, context string) {
	if resp.StatusCode == http.StatusUnauthorized {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	g.Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized),
		"%s: expected 401 from the authentication layer, got %d. Body: %s",
		context, resp.StatusCode, string(body))
}

// ExpectBodyNotToContain asserts the response body does not contain the given
// substring, and returns the body so callers can make further assertions on
// it. Use to check a denial did not leak resource metadata, or that a secret
// value never appears in a response.
func ExpectBodyNotToContain(g Gomega, resp *http.Response, needle, context string) string {
	body, err := io.ReadAll(resp.Body)
	g.Expect(err).NotTo(HaveOccurred(), "%s: read response body", context)
	g.Expect(string(body)).NotTo(ContainSubstring(needle),
		"%s: response leaked %q", context, needle)
	return string(body)
}
