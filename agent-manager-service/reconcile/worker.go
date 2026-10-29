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

package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

const (
	renderBatch = 20
	placeBatch  = 100
	driftBatch  = 100
	// placedReason is what a user is shown while a row the placement pass placed is rendered.
	placedReason = "placed on the environment's gateway"
)

// Worker runs the core's three loops: rendering due rows, placing environment-addressed records that have no row,
// and checking deployed rows for drift.
type Worker struct {
	core   *Core
	logger *slog.Logger

	nudgeCh  chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once
	done     sync.WaitGroup
}

// NewWorker makes the core's nudges reach the worker.
func NewWorker(core *Core, logger *slog.Logger) *Worker {
	w := &Worker{core: core, logger: logger, nudgeCh: make(chan struct{}, 1), stopCh: make(chan struct{})}
	core.nudge = w.Nudge
	return w
}

// Start runs the render loop on its short interval and on nudges, the fleet-wide placement pass at once and then on
// its slow interval, and the drift check on its own slow interval.
func (w *Worker) Start(ctx context.Context) error {
	w.done.Add(3)
	go w.loop(ctx, w.core.cfg.RenderInterval, w.nudgeCh, w.RunOnce)
	go func() {
		w.placeAll(ctx)
		w.loop(ctx, w.core.cfg.PlaceInterval, nil, w.placeAll)
	}()
	go w.driftLoop(ctx)
	w.logger.Info("Reconcile worker started", "renderInterval", w.core.cfg.RenderInterval,
		"placeInterval", w.core.cfg.PlaceInterval, "driftInterval", w.core.cfg.DriftInterval)
	return nil
}

// Stop ends the loops and waits for the work in progress.
func (w *Worker) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.done.Wait()
		w.logger.Info("Reconcile worker stopped")
	})
	return nil
}

// Nudge runs the render loop now, for a render a request just made owed.
func (w *Worker) Nudge() {
	select {
	case w.nudgeCh <- struct{}{}:
	default:
	}
}

func (w *Worker) loop(ctx context.Context, interval time.Duration, wake <-chan struct{}, run func(context.Context)) {
	defer w.done.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			run(ctx)
		case <-wake:
			run(ctx)
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RunOnce renders every row due now, a batch at a time, each render of a batch in parallel.
func (w *Worker) RunOnce(ctx context.Context) {
	for {
		claims, err := w.core.repo.ClaimDueRenders(ctx, w.core.now(), w.core.cfg.Lease, renderBatch)
		if err != nil {
			w.logger.Error("Failed to claim due renders", "error", err)
			return
		}
		var wg sync.WaitGroup
		for _, claim := range claims {
			wg.Go(func() {
				if err := w.core.renderClaim(ctx, claim); err != nil {
					w.logger.Error("Failed to write back a render", "artifactUUID", claim.ArtifactUUID,
						"gatewayUUID", claim.GatewayUUID, "error", err)
				}
			})
		}
		wg.Wait()
		if len(claims) < renderBatch {
			return
		}
	}
}

// PlaceEnv places the environment's unplaced records on their gateways and renders what it placed. It runs after a
// gateway is mapped to the environment.
func (w *Worker) PlaceEnv(ctx context.Context, ouID string, env uuid.UUID) error {
	return w.place(ctx, ouID, &env)
}

// PlaceAll places every unplaced record in the fleet: it covers crashes and gateway role changes made outside a
// mapping.
func (w *Worker) PlaceAll(ctx context.Context) error {
	return w.place(ctx, "", nil)
}

func (w *Worker) placeAll(ctx context.Context) {
	if err := w.PlaceAll(ctx); err != nil {
		w.logger.Error("Placement pass failed", "error", err)
	}
}

func (w *Worker) place(ctx context.Context, ouID string, env *uuid.UUID) error {
	var errs []error
	for _, k := range w.core.kinds {
		kind, ok := k.(EnvironmentKind)
		if !ok {
			continue
		}
		for afterKey := ""; ; {
			records, err := kind.UnplacedRecords(ctx, ouID, env, afterKey, placeBatch)
			if err != nil {
				errs = append(errs, err)
				break
			}
			for _, record := range records {
				errs = append(errs, w.placeRecord(ctx, kind, record))
			}
			if len(records) < placeBatch {
				break
			}
			afterKey = records[len(records)-1].Key
		}
	}
	return errors.Join(errs...)
}

// placeRecord places one record on its gateway and renders it at once. A record whose environment has no
// suitable gateway is left until one is mapped.
func (w *Worker) placeRecord(ctx context.Context, kind EnvironmentKind, record Record) error {
	gateway, ok, err := w.core.target(ctx, kind, record)
	if err != nil || !ok {
		return err
	}
	p := models.Placement{ArtifactRef: record.ArtifactRef, GatewayUUID: gateway}
	inserted, err := w.core.repo.InsertPending(ctx, nil, p, placedReason)
	if err != nil || !inserted {
		return err
	}
	return w.core.renderOwed(ctx, p.OUID, p.ArtifactUUID, []uuid.UUID{gateway})
}

// driftLoop checks for drift on interval boundaries, so the replicas' ticks line up and the scan lock leaves
// one scan per interval.
func (w *Worker) driftLoop(ctx context.Context) {
	defer w.done.Done()
	interval := w.core.cfg.DriftInterval
	for {
		untilBoundary := interval - time.Duration(time.Now().UnixNano()%int64(interval))
		select {
		case <-time.After(untilBoundary):
			if _, err := w.CheckDrift(ctx); err != nil {
				w.logger.Error("Drift check failed", "error", err)
			}
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// CheckDrift marks DEPLOYED rows whose inputs moved for a re-render, for every kind with a DriftChecker. One
// replica scans at a time: scanned is false when another holds the scan.
func (w *Worker) CheckDrift(ctx context.Context) (scanned bool, err error) {
	tx := w.core.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return false, tx.Error
	}
	defer tx.Rollback()
	locked, err := repositories.TryLockDriftScan(tx)
	if err != nil || !locked {
		return false, err
	}

	drifted := 0
	for _, kind := range w.core.kinds {
		checker, ok := kind.(DriftChecker)
		if !ok {
			continue
		}
		for _, artifactKind := range artifactKindsOrName(kind) {
			n, err := w.checkDrift(ctx, checker, artifactKind)
			drifted += n
			if err != nil {
				return true, err
			}
		}
	}
	if drifted > 0 {
		w.logger.Info("Re-rendering drifted gateway deployments", "count", drifted)
		w.Nudge()
	}
	return true, tx.Commit().Error
}

func (w *Worker) checkDrift(ctx context.Context, checker DriftChecker, artifactKind string) (int, error) {
	drifted := 0
	for afterKey := ""; ; {
		placements, err := w.core.repo.ListDeployedForDrift(ctx, artifactKind, afterKey, driftBatch)
		if err != nil {
			return drifted, err
		}
		for _, p := range placements {
			reason, isDrifted, err := checker.Drifted(ctx, p)
			if err != nil {
				w.logger.Warn("Couldn't check a deployment for drift", "artifactUUID", p.ArtifactUUID,
					"gatewayUUID", p.GatewayUUID, "error", err)
				continue
			}
			if !isDrifted {
				continue
			}
			if _, err := w.core.repo.QueueRender(ctx, nil, p.OUID, p.ArtifactUUID, []uuid.UUID{p.GatewayUUID}, reason,
				repositories.QueueScopePlaced); err != nil {
				return drifted, err
			}
			drifted++
		}
		if len(placements) < driftBatch {
			return drifted, nil
		}
		afterKey = repositories.DriftKey(placements[len(placements)-1])
	}
}
