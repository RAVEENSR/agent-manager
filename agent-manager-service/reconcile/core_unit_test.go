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
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/wso2/agent-manager/agent-manager-service/eventhub"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// stubKind deploys MCP proxies and their mappings, which the gateway knows as one kind.
type stubKind struct{}

func (stubKind) Name() string { return "Mcp" }
func (stubKind) Render(context.Context, models.ArtifactRef) (Rendered, error) {
	return Rendered{}, nil
}

func (stubKind) GatewayArtifactID(ref models.ArtifactRef) string { return ref.ArtifactUUID.String() }
func (stubKind) DeployEvent(EventRef) eventhub.Event             { return eventhub.Event{} }
func (stubKind) UndeployEvent(EventRef) eventhub.Event           { return eventhub.Event{} }
func (stubKind) DeleteEvent(models.ArtifactRef) eventhub.Event   { return eventhub.Event{} }
func (stubKind) RendersInline() bool                             { return true }
func (stubKind) ArtifactKinds() []string {
	return []string{models.KindMCPProxy, models.KindMCPMapping}
}

func newUnitCore(kinds ...Kind) *Core {
	return NewCore(nil, nil, nil, kinds, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGatewayArtifactAsksTheKind(t *testing.T) {
	core := newUnitCore(stubKind{})
	ref := models.ArtifactRef{ArtifactUUID: uuid.New(), Kind: models.KindMCPMapping, Handle: "h"}

	kind, id, ok := core.GatewayArtifact(ref)

	assert.True(t, ok)
	assert.Equal(t, "Mcp", kind)
	assert.Equal(t, ref.ArtifactUUID.String(), id)
	_, _, ok = core.GatewayArtifact(models.ArtifactRef{Kind: "RestApi"})
	assert.False(t, ok)
}

func TestAKindIsRegisteredOnce(t *testing.T) {
	assert.Panics(t, func() { newUnitCore(stubKind{}, stubKind{}) })
}

func TestAfterLiveSkipsKindsWithNoHook(t *testing.T) {
	core := newUnitCore(stubKind{})

	err := core.AfterLive()(context.Background(), nil, models.Placement{ArtifactRef: models.ArtifactRef{Kind: models.KindMCPProxy}}, uuid.New())

	assert.NoError(t, err)
}

func TestMergeDeleteEventsOrdersByGateway(t *testing.T) {
	gateways := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	slices.SortFunc(gateways, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	g1, g2, g3 := gateways[0], gateways[1], gateways[2]
	e := func(id string) eventhub.Event { return eventhub.Event{EventID: id} }
	a := DeleteEvents{events: []addressedEvent{{g1, e("e1")}, {g3, e("e2")}}}
	b := DeleteEvents{events: []addressedEvent{{g1, e("e3")}, {g2, e("e4")}, {g3, e("e5")}}}

	merged := MergeDeleteEvents(a, b)

	var gotGateways []uuid.UUID
	var gotEvents []string
	for _, ev := range merged.events {
		gotGateways = append(gotGateways, ev.gateway)
		gotEvents = append(gotEvents, ev.event.EventID)
	}
	assert.Equal(t, []uuid.UUID{g1, g1, g2, g3, g3}, gotGateways)
	assert.Equal(t, []string{"e1", "e3", "e4", "e2", "e5"}, gotEvents)
	assert.NoError(t, MergeDeleteEvents().Publish(context.Background(), nil))
}
