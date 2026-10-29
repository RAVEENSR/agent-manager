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
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/eventhub"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// Config sets the worker's intervals, the render claim's lease and the render attempt budget.
type Config struct {
	RenderInterval time.Duration
	PlaceInterval  time.Duration
	DriftInterval  time.Duration
	// Lease is how long a claimed render is held; a render is cut off at half of it.
	Lease time.Duration
	// AttemptBudget is how many failed renders give up an owed deploy. Waiting for an input never counts.
	AttemptBudget int
}

// Kinds are the kinds the core reconciles.
type Kinds []Kind

// Applied is what a deploy did. A kind rendered inline names its new deployment. NoGateway means the environment
// has no suitable gateway and nothing was written. Nudge means a render is owed to the worker: call Worker.Nudge
// after the caller's transaction commits.
type Applied struct {
	Placement    models.Placement
	DeploymentID uuid.UUID
	NoGateway    bool
	Nudge        bool
}

// Core runs deploys, undeploys, restores, deletes and renders for every kind. Each writes its gateway event last in
// the transaction that changes the status row.
type Core struct {
	db     *gorm.DB
	repo   repositories.DeploymentRepository
	hub    eventhub.EventHub
	cfg    Config
	logger *slog.Logger

	kinds      []Kind
	byArtifact map[string]Kind
	now        func() time.Time
	nudge      func()
}

// NewCore registers kinds under their names and the artifact kinds they deploy.
func NewCore(
	db *gorm.DB, repo repositories.DeploymentRepository, hub eventhub.EventHub, kinds Kinds, cfg Config, logger *slog.Logger,
) *Core {
	c := &Core{
		db: db, repo: repo, hub: hub, cfg: cfg, logger: logger,
		byArtifact: map[string]Kind{},
		now:        time.Now,
		nudge:      func() {},
	}
	for _, kind := range kinds {
		c.kinds = append(c.kinds, kind)
		for _, name := range append([]string{kind.Name()}, artifactKindsOf(kind)...) {
			if _, taken := c.byArtifact[name]; taken {
				panic(fmt.Sprintf("reconcile: two kinds deploy %q", name))
			}
			c.byArtifact[name] = kind
		}
	}
	return c
}

func artifactKindsOf(kind Kind) []string {
	if k, ok := kind.(ArtifactKinds); ok {
		return k.ArtifactKinds()
	}
	return nil
}

// artifactKindsOrName is what artifacts.kind holds for the kind's artifacts.
func artifactKindsOrName(kind Kind) []string {
	if kinds := artifactKindsOf(kind); len(kinds) > 0 {
		return kinds
	}
	return []string{kind.Name()}
}

func (c *Core) kindOf(name string) (Kind, error) {
	kind, ok := c.byArtifact[name]
	if !ok {
		return nil, fmt.Errorf("no reconciler for artifact kind %q", name)
	}
	return kind, nil
}

// GatewayArtifact is the kind and id the gateway knows the artifact by; ok is false for a kind no reconciler is
// registered for. The startup sync and the ack handler move onto it once every kind is registered.
func (c *Core) GatewayArtifact(ref models.ArtifactRef) (kindName, artifactID string, ok bool) {
	kind, found := c.byArtifact[ref.Kind]
	if !found {
		return "", "", false
	}
	return kind.Name(), kind.GatewayArtifactID(ref), true
}

// Deploy deploys an artifact on the gateway a request named.
func (c *Core) Deploy(ctx context.Context, tx *gorm.DB, p models.Placement) (*Applied, error) {
	kind, err := c.kindOf(p.Kind)
	if err != nil {
		return nil, err
	}
	return c.deployTo(ctx, tx, kind, p)
}

// DeployRecord deploys an environment-addressed record saved in tx on the gateway its environment targets.
//
// With no suitable gateway nothing is written, but a kind that renders inline still renders once, so a save that
// can't deploy is still validated. A render error from an inline kind is returned for the caller to abort its
// transaction with a 4xx; waiting for an input is not an error.
func (c *Core) DeployRecord(ctx context.Context, tx *gorm.DB, kind EnvironmentKind, record Record) (*Applied, error) {
	gateway, ok, err := c.target(ctx, kind, record)
	if err != nil {
		return nil, err
	}
	if !ok {
		if kind.RendersInline() {
			if _, err := kind.Render(db.CtxWithTx(ctx, tx), record.ArtifactRef); err != nil && !errors.Is(err, ErrInputNotReady) {
				return nil, fmt.Errorf("failed to render %s: %w", record.Kind, err)
			}
		}
		return &Applied{NoGateway: true}, nil
	}
	return c.deployTo(ctx, tx, kind, models.Placement{ArtifactRef: record.ArtifactRef, GatewayUUID: gateway})
}

func (c *Core) target(ctx context.Context, kind EnvironmentKind, record Record) (uuid.UUID, bool, error) {
	if record.RequestedGateway != nil {
		return *record.RequestedGateway, true, nil
	}
	gateway, ok, err := kind.Targets(ctx, record)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("failed to find the gateway for %s: %w", record.Kind, err)
	}
	return gateway, ok, nil
}

func (c *Core) deployTo(ctx context.Context, tx *gorm.DB, kind Kind, p models.Placement) (*Applied, error) {
	if !kind.RendersInline() {
		return c.owe(ctx, tx, p, "")
	}
	rendered, err := kind.Render(db.CtxWithTx(ctx, tx), p.ArtifactRef)
	if errors.Is(err, ErrInputNotReady) {
		return c.owe(ctx, tx, p, renderReason(kind, err))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to render %s: %w", p.Kind, err)
	}
	deployment := &models.Deployment{Name: rendered.Name, Content: rendered.Content}
	if err := c.repo.StartDeploy(ctx, tx, p, deployment); err != nil {
		return nil, err
	}
	if err := c.publishDeploy(ctx, tx, kind, p, deployment); err != nil {
		return nil, err
	}
	return &Applied{Placement: p, DeploymentID: deployment.DeploymentID}, nil
}

// owe leaves the render to the worker: a new PENDING row, or the queue due on the existing one.
func (c *Core) owe(ctx context.Context, tx *gorm.DB, p models.Placement, reason string) (*Applied, error) {
	inserted, err := c.repo.InsertPending(ctx, tx, p, reason)
	if err != nil {
		return nil, err
	}
	if !inserted {
		if _, err := c.repo.QueueRender(ctx, tx, p.OUID, p.ArtifactUUID, []uuid.UUID{p.GatewayUUID}, reason,
			repositories.QueueScopeAny); err != nil {
			return nil, err
		}
	}
	return &Applied{Placement: p, Nudge: true}, nil
}

// Undeploy undeploys the artifact from the gateway. A row never sent to the gateway needs no event.
func (c *Core) Undeploy(ctx context.Context, tx *gorm.DB, p models.Placement) error {
	kind, err := c.kindOf(p.Kind)
	if err != nil {
		return err
	}
	sendEvent, err := c.repo.BeginUndeploy(ctx, tx, p)
	if err != nil || !sendEvent {
		return err
	}
	return c.publishUndeploy(ctx, tx, kind, p)
}

// Restore points the gateway back at an earlier deployment of the artifact. It clears the render queue, so a
// waiting redeploy can't overwrite it.
func (c *Core) Restore(ctx context.Context, tx *gorm.DB, p models.Placement, deploymentID uuid.UUID) error {
	kind, err := c.kindOf(p.Kind)
	if err != nil {
		return err
	}
	if err := c.repo.StartRestore(ctx, tx, p, deploymentID); err != nil {
		return err
	}
	repo := c.repo.WithTx(tx)
	deployment, err := repo.GetWithStateCtx(ctx, deploymentID.String(), p.ArtifactUUID.String(), p.OUID)
	if err != nil {
		return fmt.Errorf("failed to read the restored deployment: %w", err)
	}
	performedAt, err := c.performedAt(ctx, tx, p)
	if err != nil {
		return err
	}
	return c.publish(ctx, tx, p.GatewayUUID, kind.DeployEvent(EventRef{
		Placement: p, DeploymentID: deploymentID.String(), DeploymentName: deployment.Name, PerformedAt: performedAt,
	}))
}

// DeleteEvents are the delete events of an artifact, built before it is deleted.
type DeleteEvents struct {
	core   *Core
	events []addressedEvent
}

type addressedEvent struct {
	gateway uuid.UUID
	event   eventhub.Event
}

// Publish writes the delete events in tx, the deleting transaction, after the delete. Gateways are locked in
// gateway order.
func (d DeleteEvents) Publish(ctx context.Context, tx *gorm.DB) error {
	for _, e := range d.events {
		if err := d.core.publish(ctx, tx, e.gateway, e.event); err != nil {
			return err
		}
	}
	return nil
}

// PrepareDelete builds the artifact's delete events for every gateway it was ever sent, before its row and status
// rows cascade away. The caller deletes, then publishes them last in the same transaction.
func (c *Core) PrepareDelete(ctx context.Context, tx *gorm.DB, ref models.ArtifactRef) (DeleteEvents, error) {
	kind, err := c.kindOf(ref.Kind)
	if err != nil {
		return DeleteEvents{}, err
	}
	gateways, err := c.repo.WithTx(tx).SentGateways(ctx, ref.OUID, ref.ArtifactUUID)
	if err != nil {
		return DeleteEvents{}, err
	}
	events := DeleteEvents{core: c}
	for _, gateway := range gateways {
		gatewayUUID, err := uuid.Parse(gateway)
		if err != nil {
			return DeleteEvents{}, fmt.Errorf("invalid gateway id %q: %w", gateway, err)
		}
		events.events = append(events.events, addressedEvent{gateway: gatewayUUID, event: kind.DeleteEvent(ref)})
	}
	return events, nil
}

// MarkStale marks a redeploy owed on the artifact's placed rows, on gateways or, when empty, on all of them, because
// an input outside the artifact changed. A kind that renders inline is rendered now, each row in its own
// transaction; any other kind is left to the worker.
func (c *Core) MarkStale(ctx context.Context, kind, ouID string, artifactUUID uuid.UUID, gateways []uuid.UUID, reason string) error {
	k, err := c.kindOf(kind)
	if err != nil {
		return err
	}
	queued, err := c.repo.QueueRender(ctx, nil, ouID, artifactUUID, gateways, reason, repositories.QueueScopePlaced)
	if err != nil || queued == 0 {
		return err
	}
	if !k.RendersInline() {
		c.nudge()
		return nil
	}
	return c.renderOwed(ctx, ouID, artifactUUID, gateways)
}

// renderOwed claims and renders the artifact's due rows on gateways (all when empty).
func (c *Core) renderOwed(ctx context.Context, ouID string, artifactUUID uuid.UUID, gateways []uuid.UUID) error {
	claims, err := c.repo.ClaimDueRendersOf(ctx, ouID, artifactUUID, gateways, c.now(), c.cfg.Lease)
	if err != nil {
		return err
	}
	var errs []error
	for _, claim := range claims {
		errs = append(errs, c.renderClaim(ctx, claim))
	}
	return errors.Join(errs...)
}

// Status is what the user is shown for the artifact on the gateway; ErrNotPlaced when it has no row there.
func (c *Core) Status(ctx context.Context, p models.Placement) (models.UserStatus, error) {
	row, err := c.repo.GetStatusRecord(ctx, p)
	if err != nil {
		return models.UserStatus{}, err
	}
	if row == nil {
		return models.UserStatus{}, ErrNotPlaced
	}
	return models.UserStatusOf(*row), nil
}

// AfterLive is the ack handler's hook: it runs the kind's AfterLiveHook, if it has one, when a row goes DEPLOYED.
func (c *Core) AfterLive() repositories.AfterLiveFunc {
	return func(ctx context.Context, tx *gorm.DB, p models.Placement, deploymentID uuid.UUID) error {
		hook, ok := c.byArtifact[p.Kind].(AfterLiveHook)
		if !ok {
			return nil
		}
		return hook.AfterLive(ctx, tx, p, deploymentID)
	}
}

// renderClaim renders a claimed row outside any transaction and writes back what came of it. A write-back the
// claim no longer holds the token for is dropped: a newer request, restore or undeploy owns the row.
func (c *Core) renderClaim(ctx context.Context, claim models.ClaimedRender) error {
	log := c.logger.With("artifactUUID", claim.ArtifactUUID, "kind", claim.Kind, "gatewayUUID", claim.GatewayUUID)
	kind, ok := c.byArtifact[claim.Kind]
	if !ok {
		next := claim.ClaimedAt.Add(maxBackoff)
		reason := fmt.Sprintf("no reconciler for artifact kind %q", claim.Kind)
		log.Warn("Leaving an owed render of an unregistered kind for later")
		return c.superseded(log, c.repo.RecordRenderOutcome(ctx, claim,
			repositories.RenderOutcome{Outcome: repositories.RenderWaiting, Reason: reason, NextAttemptAt: &next}))
	}

	renderCtx, cancel := context.WithTimeout(ctx, c.cfg.Lease/2)
	rendered, err := kind.Render(renderCtx, claim.ArtifactRef)
	cancel()
	switch {
	case err == nil:
		return c.superseded(log, c.writeBack(ctx, kind, claim, rendered))
	case errors.Is(err, ErrRecordGone):
		log.Info("The artifact's record is gone; undeploying the row instead of deploying it")
		return c.superseded(log, c.abandon(ctx, kind, claim))
	default:
		outcome := renderOutcome(err, renderReason(kind, err), claim, c.cfg.AttemptBudget)
		log.Warn("Render didn't produce a deployment", "outcome", outcome.Outcome, "attempt", claim.AttemptCount, "error", err)
		return c.superseded(log, c.repo.RecordRenderOutcome(ctx, claim, outcome))
	}
}

func (c *Core) superseded(log *slog.Logger, err error) error {
	if errors.Is(err, repositories.ErrRenderSuperseded) {
		log.Info("Render superseded by a newer request, restore or undeploy; dropping it")
		return nil
	}
	return err
}

func (c *Core) writeBack(ctx context.Context, kind Kind, claim models.ClaimedRender, rendered Rendered) error {
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deployment := &models.Deployment{Name: rendered.Name, Content: rendered.Content}
		if err := c.repo.CompleteRender(ctx, tx, claim, deployment); err != nil {
			return err
		}
		return c.publishDeploy(ctx, tx, kind, claim.Placement, deployment)
	})
}

func (c *Core) abandon(ctx context.Context, kind Kind, claim models.ClaimedRender) error {
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sendEvent, err := c.repo.AbandonRender(ctx, tx, claim)
		if err != nil || !sendEvent {
			return err
		}
		return c.publishUndeploy(ctx, tx, kind, claim.Placement)
	})
}

func (c *Core) publishDeploy(ctx context.Context, tx *gorm.DB, kind Kind, p models.Placement, deployment *models.Deployment) error {
	return c.publish(ctx, tx, p.GatewayUUID, kind.DeployEvent(EventRef{
		Placement: p, DeploymentID: deployment.DeploymentID.String(), DeploymentName: deployment.Name,
		PerformedAt: *deployment.UpdatedAt,
	}))
}

func (c *Core) publishUndeploy(ctx context.Context, tx *gorm.DB, kind Kind, p models.Placement) error {
	performedAt, err := c.performedAt(ctx, tx, p)
	if err != nil {
		return err
	}
	return c.publish(ctx, tx, p.GatewayUUID, kind.UndeployEvent(EventRef{Placement: p, PerformedAt: performedAt}))
}

// performedAt reads back the transition the caller just started in tx, for its event.
func (c *Core) performedAt(ctx context.Context, tx *gorm.DB, p models.Placement) (time.Time, error) {
	row, err := c.repo.WithTx(tx).GetStatusRecord(ctx, p)
	if err != nil {
		return time.Time{}, err
	}
	if row == nil {
		return time.Time{}, ErrNotPlaced
	}
	return row.PerformedAt, nil
}

// publish writes the event in tx. It must be the transaction's last write: it holds the gateway's event lock
// until commit.
func (c *Core) publish(ctx context.Context, tx *gorm.DB, gateway uuid.UUID, event eventhub.Event) error {
	event.GatewayID = gateway.String()
	if err := c.hub.PublishEventTx(ctx, tx, event.GatewayID, event); err != nil {
		return fmt.Errorf("failed to publish %s event: %w", event.EventType, err)
	}
	return nil
}
