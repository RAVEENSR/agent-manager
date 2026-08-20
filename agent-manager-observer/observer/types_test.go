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

package observer

import (
	"encoding/json"
	"testing"
)

func TestSpanStatusUnmarshalJSON(t *testing.T) {
	t.Run("legacy string form (adapter 0.5.x)", func(t *testing.T) {
		var s SpanStatus
		if err := json.Unmarshal([]byte(`"error"`), &s); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Code != "error" || s.Message != "" {
			t.Errorf("got %+v, want {Code:error Message:}", s)
		}
	})

	t.Run("object form (0.6.0)", func(t *testing.T) {
		var s SpanStatus
		if err := json.Unmarshal([]byte(`{"code":"Error","message":"boom"}`), &s); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Code != "Error" || s.Message != "boom" {
			t.Errorf("got %+v, want {Code:Error Message:boom}", s)
		}
	})

	t.Run("null leaves pointer nil", func(t *testing.T) {
		var info SpanInfo
		if err := json.Unmarshal([]byte(`{"spanId":"a","status":null}`), &info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != nil {
			t.Errorf("expected nil status, got %+v", info.Status)
		}
	})
}

// A single legacy string-shaped status must not fail the whole batch decode
// (client.go unmarshals the entire spans-query response at once).
func TestTraceSpansQueryResponseMixedStatusShapes(t *testing.T) {
	payload := `{
		"spans": [
			{"spanId":"a","status":"error"},
			{"spanId":"b","status":{"code":"Error","message":"boom"}},
			{"spanId":"c"}
		],
		"total": 3
	}`

	var resp TraceSpansQueryResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("mixed-shape batch failed to decode: %v", err)
	}
	if len(resp.Spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(resp.Spans))
	}
	if resp.Spans[0].Status == nil || resp.Spans[0].Status.Code != "error" {
		t.Errorf("span a: got %+v, want status code error", resp.Spans[0].Status)
	}
	if resp.Spans[1].Status == nil || resp.Spans[1].Status.Message != "boom" {
		t.Errorf("span b: got %+v, want message boom", resp.Spans[1].Status)
	}
	if resp.Spans[2].Status != nil {
		t.Errorf("span c: expected nil status, got %+v", resp.Spans[2].Status)
	}
}
