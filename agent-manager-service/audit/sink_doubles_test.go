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
	"sync"
)

// Test doubles for the Sink contract. They live in a test file so they are not
// compiled into the service binary.

// memorySink retains events. Test-only, but it lives here so the Sink contract
// has an in-tree reference implementation.
type memorySink struct {
	mu     sync.Mutex
	events []Event
}

// NewMemorySink returns a sink that retains everything written to it.
func NewMemorySink() *memorySink { //nolint:revive // returned concrete for test assertions
	return &memorySink{}
}

func (m *memorySink) Name() string { return "memory" }

func (m *memorySink) Write(_ context.Context, events []Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	return nil
}

// Events returns a copy of everything recorded so far.
func (m *memorySink) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Reset discards retained events.
func (m *memorySink) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}

// failingSink always errors. Used to exercise the fail-closed path.
type failingSink struct{ err error }

// NewFailingSink returns a sink that fails every write with err.
func NewFailingSink(err error) Sink { return failingSink{err: err} }

func (f failingSink) Name() string                         { return "failing" }
func (f failingSink) Write(context.Context, []Event) error { return f.err }
