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
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// quietLogger discards output so a test that deliberately triggers error paths
// does not spew into the test log.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecorderFlushesOnClose(t *testing.T) {
	sink := NewMemorySink()
	// A long flush interval proves Close drains rather than the ticker firing.
	rec := NewRecorder(sink, quietLogger(), Config{BufferSize: 16, BatchSize: 8, FlushInterval: time.Hour})

	ctx := context.Background()
	for range 3 {
		rec.Record(ctx, Event{Action: "agent:create"})
	}

	if err := rec.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(sink.Events()); got != 3 {
		t.Errorf("recorded %d events, want 3", got)
	}
}

func TestRecorderFlushesWhenBatchIsFull(t *testing.T) {
	sink := NewMemorySink()
	rec := NewRecorder(sink, quietLogger(), Config{BufferSize: 16, BatchSize: 2, FlushInterval: time.Hour})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })

	ctx := context.Background()
	for range 4 {
		rec.Record(ctx, Event{Action: "agent:create"})
	}

	waitFor(t, func() bool { return len(sink.Events()) >= 4 })
}

func TestRecorderFlushesOnInterval(t *testing.T) {
	sink := NewMemorySink()
	// A batch size larger than the event count forces the ticker to be what flushes.
	rec := NewRecorder(sink, quietLogger(), Config{BufferSize: 16, BatchSize: 100, FlushInterval: 10 * time.Millisecond})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })

	rec.Record(context.Background(), Event{Action: "agent:create"})

	waitFor(t, func() bool { return len(sink.Events()) == 1 })
}

// TestRecordNeverBlocksWhenBufferIsFull is the availability guarantee. An audit
// backlog must degrade to dropped records, never to a stalled request — the
// alternative turns a slow sink into an outage.
func TestRecordNeverBlocksWhenBufferIsFull(t *testing.T) {
	// A sink that blocks forever, so the worker cannot drain the buffer.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	rec := NewRecorder(blockingSink{release: blocked}, quietLogger(),
		Config{BufferSize: 1, BatchSize: 1, FlushInterval: time.Hour})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more events than the buffer can hold.
		for range 100 {
			rec.Record(context.Background(), Event{Action: "agent:create"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked when the buffer was full; it must drop instead")
	}

	buffered, ok := rec.(*bufferedRecorder)
	if !ok {
		t.Fatalf("expected *bufferedRecorder, got %T", rec)
	}
	if buffered.dropped.Load() == 0 {
		t.Error("dropped counter was not incremented, so the loss would be invisible")
	}
}

func TestRecordAfterCloseIsIgnored(t *testing.T) {
	sink := NewMemorySink()
	rec := NewRecorder(sink, quietLogger(), Config{BufferSize: 4, BatchSize: 1, FlushInterval: time.Hour})

	ctx := context.Background()
	if err := rec.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Must not panic on a closed channel.
	rec.Record(ctx, Event{Action: "agent:create"})

	if got := len(sink.Events()); got != 0 {
		t.Errorf("recorded %d events after Close, want 0", got)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	rec := NewRecorder(NewMemorySink(), quietLogger(), Config{})

	ctx := context.Background()
	if err := rec.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// A second Close must not panic by re-closing the channel.
	if err := rec.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestRecordSyncReportsSinkFailure is the fail-closed path: a caller that must
// not proceed unrecorded relies on this error surfacing.
func TestRecordSyncReportsSinkFailure(t *testing.T) {
	sentinel := errors.New("sink is down")
	rec := NewRecorder(NewFailingSink(sentinel), quietLogger(), Config{})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })

	err := rec.RecordSync(context.Background(), Event{Action: "git-secret:create"})
	if !errors.Is(err, sentinel) {
		t.Errorf("RecordSync error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestRecordSyncWritesImmediately(t *testing.T) {
	sink := NewMemorySink()
	rec := NewRecorder(sink, quietLogger(), Config{FlushInterval: time.Hour})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })

	if err := rec.RecordSync(context.Background(), Event{Action: "git-secret:create"}); err != nil {
		t.Fatalf("RecordSync: %v", err)
	}
	if got := len(sink.Events()); got != 1 {
		t.Errorf("RecordSync recorded %d events synchronously, want 1", got)
	}
}

// TestRecordSyncSurvivesCancelledContext matters because the fail-closed path
// runs while a request may already be cancelled; the record still has to land.
func TestRecordSyncSurvivesCancelledContext(t *testing.T) {
	sink := NewMemorySink()
	rec := NewRecorder(sink, quietLogger(), Config{})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := rec.RecordSync(ctx, Event{Action: "git-secret:create"}); err != nil {
		t.Fatalf("RecordSync with a cancelled context: %v", err)
	}
	if got := len(sink.Events()); got != 1 {
		t.Errorf("recorded %d events, want 1", got)
	}
}

// TestPrepareStampsIdentityAndDefaults covers the invariants every sink can rely
// on, whichever path produced the event.
func TestPrepareStampsIdentityAndDefaults(t *testing.T) {
	e := Event{Action: "agent:create"}
	prepare(&e)

	if e.EventID == uuid.Nil {
		t.Error("EventID was not assigned")
	}
	if e.OccurredAt.IsZero() {
		t.Error("OccurredAt was not assigned")
	}
	if e.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", e.SchemaVersion, SchemaVersion)
	}
	if e.ActionClass != ClassConfig {
		t.Errorf("ActionClass = %q, want it derived from the action", e.ActionClass)
	}
	if e.Outcome != OutcomeSuccess {
		t.Errorf("Outcome = %q, want success by default", e.Outcome)
	}
	if e.ActorType != ActorAnonymous {
		t.Errorf("ActorType = %q, want anonymous by default", e.ActorType)
	}
}

func TestPreparePreservesExplicitValues(t *testing.T) {
	id := uuid.New()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	e := Event{
		Action:     "agent:create",
		EventID:    id,
		OccurredAt: when,
		Outcome:    OutcomeUnknown,
		ActorType:  ActorSystem,
		Severity:   SeverityCritical,
	}
	prepare(&e)

	if e.EventID != id {
		t.Error("prepare overwrote an explicit EventID, breaking Begin/Complete linkage")
	}
	if !e.OccurredAt.Equal(when) {
		t.Error("prepare overwrote an explicit OccurredAt")
	}
	if e.Outcome != OutcomeUnknown {
		t.Errorf("Outcome = %q, want the explicit unknown to survive", e.Outcome)
	}
	if e.Severity != SeverityCritical {
		t.Errorf("Severity = %d, want the explicit value to survive", e.Severity)
	}
}

// TestNoopRecorderRecordSyncSucceeds confirms that disabling auditing does not
// break fail-closed call sites: an operator who turned the trail off should not
// have privileged operations start refusing.
func TestNoopRecorderRecordSyncSucceeds(t *testing.T) {
	if err := NewNoopRecorder().RecordSync(context.Background(), Event{Action: "git-secret:create"}); err != nil {
		t.Errorf("noop RecordSync = %v, want nil", err)
	}
}

// TestUninstalledRecorderFailsSync is the opposite case: a *missing* recorder is
// a wiring defect, not a choice, so a fail-closed site must refuse rather than
// proceed with no record.
func TestUninstalledRecorderFailsSync(t *testing.T) {
	rec := FromContext(context.Background())

	if rec == nil {
		t.Fatal("FromContext returned nil; emit sites would panic")
	}
	if err := rec.RecordSync(context.Background(), Event{Action: "git-secret:create"}); !errors.Is(err, ErrRecorderUnavailable) {
		t.Errorf("RecordSync = %v, want ErrRecorderUnavailable", err)
	}
	// Record must stay non-fatal so an unaudited read path cannot crash.
	rec.Record(context.Background(), Event{Action: "agent:create"})
}

func TestMultiSinkWritesToAllEvenWhenOneFails(t *testing.T) {
	good := NewMemorySink()
	sentinel := errors.New("boom")

	err := NewMultiSink(NewFailingSink(sentinel), good).Write(
		context.Background(), []Event{{Action: "agent:create"}},
	)

	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to report the failing sink", err)
	}
	if got := len(good.Events()); got != 1 {
		t.Errorf("healthy sink recorded %d events, want 1; one bad sink must not block another", got)
	}
}

func TestNewMultiSinkDegenerateCases(t *testing.T) {
	if got := NewMultiSink().Name(); got != "discard" {
		t.Errorf("no sinks should yield a discarding sink, got %q", got)
	}
	only := NewMemorySink()
	if got := NewMultiSink(only).Name(); got != only.Name() {
		t.Errorf("a single sink should be returned unwrapped, got %q", got)
	}
}

// blockingSink never completes a write until released.
type blockingSink struct{ release chan struct{} }

func (blockingSink) Name() string { return "blocking" }

func (b blockingSink) Write(context.Context, []Event) error {
	<-b.release
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// TestRecordDuringCloseDoesNotPanic pins the shutdown race.
//
// Close used to close the event channel while request goroutines were still
// sending on it. The atomic guard in Record could not prevent that: checking a
// flag and sending are two steps, and Close can land between them. In
// production the window is wide open — the main server returns from Shutdown on
// timeout with handlers still running, and background workers are never joined
// — so an ordinary graceful shutdown could panic mid-request, or kill the
// process outright from a worker goroutine with no recover.
func TestRecordDuringCloseDoesNotPanic(t *testing.T) {
	for range 200 {
		rec := NewRecorder(NewMemorySink(), quietLogger(), Config{BufferSize: 1, BatchSize: 1})

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 50 {
					// A panic here fails the test: it crashes the test binary.
					rec.Record(context.Background(), Event{Action: "agent:create"})
				}
			}()
		}

		_ = rec.Close(context.Background())
		wg.Wait()
	}
}
