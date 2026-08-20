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

package services

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// Internal and external consumers both reach the gateway through its vhost. The stored
// RuntimeURL is deliberately not used: it is set here to pin that it stays unused, so a
// reintroduced in-cluster address for internal agents is caught rather than silently shipped.
func TestBuildProxyURLUsesVhostForInternalAndExternal(t *testing.T) {
	contextPath := "/llm/proxy"
	gateway := &models.Gateway{
		Name:       "api-platform-acme-dev",
		Vhost:      "https://dev-acme.gateway.example.com",
		RuntimeURL: "http://api-platform-acme-dev-gw-gateway-gateway-runtime.acme-dev:22893",
	}

	require.Equal(
		t,
		"https://dev-acme.gateway.example.com/llm/proxy",
		buildProxyURL(gateway, &contextPath, true),
	)
	require.Equal(
		t,
		"https://dev-acme.gateway.example.com/llm/proxy",
		buildProxyURL(gateway, &contextPath, false),
	)
}

// An absent RuntimeURL is no longer a misconfiguration for either consumer, so neither
// branch may log: an ERROR here would page someone about a field nothing reads.
func TestBuildProxyURLIsSilentWhenRuntimeURLEmpty(t *testing.T) {
	contextPath := "/llm/proxy"
	gateway := &models.Gateway{
		UUID:  uuid.New(),
		Name:  "default",
		Vhost: "https://dev-acme.gateway.example.com",
	}

	logs := captureDefaultLogger(t)

	require.Equal(t, "https://dev-acme.gateway.example.com/llm/proxy", buildProxyURL(gateway, &contextPath, true))
	require.Empty(t, logs.String())

	logs.Reset()
	require.Equal(t, "https://dev-acme.gateway.example.com/llm/proxy", buildProxyURL(gateway, &contextPath, false))
	require.Empty(t, logs.String())
}

// captureDefaultLogger redirects slog's default logger into a buffer for the duration of the
// test, restoring the previous default unconditionally so no capture leaks into sibling tests.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestBuildProxyURLWithoutContextPath(t *testing.T) {
	gateway := &models.Gateway{
		Vhost:      "https://dev-acme.gateway.example.com",
		RuntimeURL: "http://runtime.acme-dev:22893",
	}

	require.Equal(t, "https://dev-acme.gateway.example.com", buildProxyURL(gateway, nil, true))
}

func TestBuildMCPProxyURLUsesVhostForInternalAndExternal(t *testing.T) {
	contextPath := "  /tools/  "
	gateway := &models.Gateway{
		Name:       "api-platform-acme-dev",
		Vhost:      "https://dev-acme.gateway.example.com/",
		RuntimeURL: "http://api-platform-acme-dev-gw-gateway-gateway-runtime.acme-dev:22893",
	}

	require.Equal(
		t,
		"https://dev-acme.gateway.example.com/tools/mcp",
		buildMCPProxyURL(gateway, &contextPath, true),
	)
	require.Equal(
		t,
		"https://dev-acme.gateway.example.com/tools/mcp",
		buildMCPProxyURL(gateway, &contextPath, false),
	)
}

func TestBuildMCPProxyURLFallsBackToVhostWhenRuntimeURLEmpty(t *testing.T) {
	gateway := &models.Gateway{
		Name:  "custom-gateway",
		Vhost: "https://gateway.example.com/",
	}

	require.Equal(t, "https://gateway.example.com/mcp", buildMCPProxyURL(gateway, nil, true))
}
