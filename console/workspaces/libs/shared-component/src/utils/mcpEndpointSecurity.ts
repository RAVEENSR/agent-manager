/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import type { MCPEndpointConfig } from "@agent-management-platform/types";

/** How one MCP endpoint is secured. `""` is the "None" option in the UI. */
export type AuthenticationType = "apiKey" | "identity" | "";

const AUTHENTICATION_TYPE_LABELS: Record<AuthenticationType, string> = {
  "": "None",
  apiKey: "API Key",
  identity: "OAuth",
};

// Display label for an AuthenticationType, shared by the Security tab's method
// selector and the Overview tab's Auth Type summary so both stay in sync.
export function getAuthenticationTypeLabel(type: AuthenticationType): string {
  return AUTHENTICATION_TYPE_LABELS[type];
}

export function isAPIKeySecurityEnabled(
  config: MCPEndpointConfig | undefined,
): boolean {
  const apiKeyConfig = config?.security?.apiKey;
  return (
    config?.security?.enabled !== false &&
    !!apiKeyConfig &&
    apiKeyConfig.enabled !== false
  );
}

// Used by resolveAuthenticationType below to derive the Security tab's
// active auth method from the endpoint's security config.
function isIdentitySecurityEnabled(
  config: MCPEndpointConfig | undefined,
): boolean {
  return (
    config?.security?.enabled !== false &&
    config?.security?.identity?.enabled === true
  );
}

/**
 * Derives which authentication method is active from the endpoint's security
 * config. The single rule behind the MCP Servers Security tab (method selector),
 * the Overview tab (Auth Type summary), and the runtime variables the agent
 * creation and configuration pages ask for.
 *
 * Note that "None" is not the absence of a security object: the Security tab
 * saves it as `enabled: true` with both `apiKey.enabled` and `identity.enabled`
 * false. Anything that infers "API key" from "not identity" therefore gets None
 * wrong, which is what issue #1597 originally exposed for OAuth.
 */
export function resolveAuthenticationType(
  config: MCPEndpointConfig | undefined,
): AuthenticationType {
  if (isAPIKeySecurityEnabled(config)) return "apiKey";
  if (isIdentitySecurityEnabled(config)) return "identity";
  return "";
}
