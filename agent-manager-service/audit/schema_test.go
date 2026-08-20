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

package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRegisteredActionsHaveDetailSchemas forces a decision about what each
// explicitly declared action may record. Without it, a new action would silently
// accept only the base fields and quietly drop everything a caller attached.
func TestRegisteredActionsHaveDetailSchemas(t *testing.T) {
	schemas := make(map[Action]bool)
	for _, a := range SchemaActions() {
		schemas[a] = true
	}

	for _, action := range RegisteredActions() {
		if !schemas[action] {
			t.Errorf("action %q is registered but has no detail schema; "+
				"add one in schema.go (an empty map is fine if it carries no details)", action)
		}
	}
}

func TestRegisteredActionsAreWellFormed(t *testing.T) {
	for _, action := range RegisteredActions() {
		name := string(action)
		if strings.Count(name, ":") != 1 {
			t.Errorf("action %q should read as <resource>:<verb>", name)
		}
		if name != strings.ToLower(name) {
			t.Errorf("action %q should be lowercase", name)
		}
		if action.Class() == "" {
			t.Errorf("action %q has no class", name)
		}
		if action.Severity() == 0 {
			t.Errorf("action %q has no severity", name)
		}
	}
}

func TestDetailSchemaAlwaysIncludesBaseFields(t *testing.T) {
	schema := DetailSchema("some:unregistered-action")

	for key := range baseFields {
		if _, ok := schema[key]; !ok {
			t.Errorf("base field %q missing from the schema of an unregistered action", key)
		}
	}
}

// TestAuthFailureRecorderRecordsClassifiedReason covers the authentication gap:
// rejections happen before route matching, so this hook is the only way they
// reach the trail at all.
func TestAuthFailureRecorderRecordsClassifiedReason(t *testing.T) {
	rec := &syncCapture{}
	hook := AuthFailureRecorder(rec)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/projects", nil)
	r.Header.Set("Authorization", "Bearer expired.token.here")
	r.RemoteAddr = "203.0.113.9:44321"

	hook(r, "expired")

	if len(rec.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(rec.events))
	}
	e := rec.events[0]
	if e.Action != ActionAuthnFailure {
		t.Errorf("Action = %q", e.Action)
	}
	if e.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %q, want deny", e.Outcome)
	}
	if e.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", e.StatusCode)
	}
	if e.ActorType != ActorAnonymous {
		t.Errorf("ActorType = %q, want anonymous", e.ActorType)
	}
	if e.SourceIP != "203.0.113.9" {
		t.Errorf("SourceIP = %q, want the client IP for correlating a credential-stuffing source", e.SourceIP)
	}
	if got, _ := e.Details["reason"].(string); got != "expired" {
		t.Errorf("reason = %q, want expired", got)
	}
}

// TestAuthFailureRecorderNeverRecordsTokenMaterial is the property that makes it
// safe to record failures at all.
func TestAuthFailureRecorderNeverRecordsTokenMaterial(t *testing.T) {
	rec := &syncCapture{}
	hook := AuthFailureRecorder(rec)

	const token = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.signature-part"
	r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/projects", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	hook(r, "bad-signature")

	if len(rec.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(rec.events))
	}
	// Serialise everything the record carries and check no fragment survived.
	var sb strings.Builder
	e := rec.events[0]
	sb.WriteString(e.RequestPath)
	sb.WriteString(e.UserAgent)
	sb.WriteString(e.ActorID)
	for _, v := range e.Details {
		if s, ok := v.(string); ok {
			sb.WriteString(s)
		}
	}
	if strings.Contains(sb.String(), "eyJ") || strings.Contains(sb.String(), "signature-part") {
		t.Errorf("token material leaked into the authn failure record: %q", sb.String())
	}
	// The presence of a header is useful; its value is not.
	if got, _ := e.Details["authHeader"].(bool); !got {
		t.Error("authHeader should record that a header was present")
	}
}

// TestAuthFailureRecorderThrottlesPerSource stops a rejected-token flood from
// turning an authentication problem into an audit-volume problem, while keeping
// the count so the signal survives.
func TestAuthFailureRecorderThrottlesPerSource(t *testing.T) {
	rec := &syncCapture{}
	hook := AuthFailureRecorder(rec)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/acme/projects", nil)
	r.RemoteAddr = "203.0.113.9:44321"

	for range maxAuthnFailuresPerWindow * 3 {
		hook(r, "expired")
	}

	if len(rec.events) > maxAuthnFailuresPerWindow {
		t.Errorf("recorded %d events, want at most %d per window",
			len(rec.events), maxAuthnFailuresPerWindow)
	}
	if len(rec.events) == 0 {
		t.Error("throttling suppressed everything; the signal must survive")
	}
}

func TestAuthFailureRecorderThrottlesIndependentlyPerSource(t *testing.T) {
	rec := &syncCapture{}
	hook := AuthFailureRecorder(rec)

	for _, ip := range []string{"203.0.113.1:1", "203.0.113.2:2"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.RemoteAddr = ip
		for range maxAuthnFailuresPerWindow * 2 {
			hook(r, "expired")
		}
	}

	// One noisy source must not consume another's budget.
	if len(rec.events) < maxAuthnFailuresPerWindow*2 {
		t.Errorf("recorded %d events, want each source to get its own allowance", len(rec.events))
	}
}

// TestAuthnLimiterReportsSuppressedCount checks the loss is quantified rather
// than silent.
func TestAuthnLimiterReportsSuppressedCount(t *testing.T) {
	limiter := newAuthnLimiter()
	now := time.Now()

	for range maxAuthnFailuresPerWindow {
		if emit, _ := limiter.allow("1.2.3.4", now); !emit {
			t.Fatal("budget exhausted early")
		}
	}
	// Over budget: suppressed.
	if emit, _ := limiter.allow("1.2.3.4", now); emit {
		t.Fatal("expected suppression once the budget is spent")
	}

	// The next window carries the suppressed tally forward.
	emit, suppressed := limiter.allow("1.2.3.4", now.Add(authnFailureWindow+time.Second))
	if !emit {
		t.Fatal("a new window should emit again")
	}
	if suppressed == 0 {
		t.Error("suppressed count was lost across the window boundary")
	}
}

func TestAuthFailureRecorderIgnoresNilInputs(t *testing.T) {
	// Must not panic when auditing is disabled or the request is absent.
	AuthFailureRecorder(nil)(httptest.NewRequest(http.MethodGet, "/x", nil), "expired")
	AuthFailureRecorder(&syncCapture{})(nil, "expired")
}

// syncCapture records synchronously so tests need no draining.
type syncCapture struct {
	events []Event
}

func (s *syncCapture) Record(_ context.Context, e Event) {
	prepare(&e)
	s.events = append(s.events, e)
}

func (s *syncCapture) RecordSync(ctx context.Context, e Event) error {
	s.Record(ctx, e)
	return nil
}

func (s *syncCapture) Close(context.Context) error { return nil }
