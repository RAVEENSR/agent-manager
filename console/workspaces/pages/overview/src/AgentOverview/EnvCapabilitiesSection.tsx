/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
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

import { useState } from "react";
import { Box, Button, Chip, Tooltip, Typography } from "@wso2/oxygen-ui";
import { Plug } from "@wso2/oxygen-ui-icons-react";
import {
    CollapsibleSection,
    DeploymentStatus,
    getAgentDeploymentPath,
    OverviewSectionCard,
} from "@agent-management-platform/shared-component";
import { type Configurations } from "@agent-management-platform/types";
import { TextInput } from "@agent-management-platform/views";
import { ConsumerConfigDrawer, type AuthMode } from "./ConsumerConfigDrawer";
import { useAgentEndpointResources } from "./useAgentEndpointResources";

interface EnvCapabilitiesSectionProps {
    orgId: string;
    projectId: string;
    agentId: string;
    envId: string;
    configurations?: Configurations;
    external?: boolean;
    deploymentStatus?: DeploymentStatus;
}

// Stable reference so an absent `oauthConfig.issuers` doesn't defeat
// ConsumerConfigDrawer's memoization with a new empty array every render.
const EMPTY_ISSUERS: string[] = [];

/**
 * Everything derived from `authMode` in one place — the Deploy page
 * (DeployCard.tsx) mirrors this same oauth/apikey/none branching for its own
 * security summary, so keeping every consumer of `authMode` here keyed off
 * one lookup (rather than four separate ternary chains) is what keeps the
 * wording in sync as it changes.
 */
function getAuthPresentation(
    authMode: AuthMode, authHeaderPrefix: string, oauthHeaderName: string,
): { label: string; tooltip: string; headerExample: string } {
    switch (authMode) {
        case "oauth":
            return {
                label: `OAuth2 (${authHeaderPrefix})`,
                tooltip: `Callers send an Authorization: ${authHeaderPrefix} <token> header validated `
                    + "by the gateway",
                headerExample: `${oauthHeaderName}: ${authHeaderPrefix} <token>`,
            };
        case "apikey":
            return {
                label: "API Key",
                tooltip: "Requests must include the header: x-api-key: <your-key>",
                headerExample: "x-api-key: <your-api-key>",
            };
        case "none":
            return {
                label: "None",
                tooltip: "Endpoint is publicly accessible without authentication",
                headerExample: "No authentication header required",
            };
    }
}

interface StatusPillProps {
    label: string;
    value: string;
    tooltip: string;
}

/** Chip badge shared by the Auth and CORS summaries below Invoke URL. */
const StatusPill: React.FC<StatusPillProps> = ({ label, value, tooltip }) => (
    <Tooltip title={tooltip}>
        <Chip
            variant="outlined"
            size="small"
            label={`${label}: ${value}`}
        />
    </Tooltip>
);

/**
 * Per-environment "API Endpoint" card — the agent's invoke URL plus a
 * read-only CORS/Authentication summary (no configure action here; that lives
 * on the Deploy page). The invoke URL is resolved from the environment's
 * deployed endpoints; the sibling "Agent Interface" card (which lists the
 * parsed HTTP resources) now lives next to the "Agent ID" card instead of
 * here — see EnvAgentInterfaceCard. Not applicable to external agents (they
 * aren't deployed through this platform, so there's nothing to fetch).
 */
export const EnvCapabilitiesSection: React.FC<EnvCapabilitiesSectionProps> = ({
    orgId, projectId, agentId, envId, configurations, external, deploymentStatus,
}) => {
    const [consumerConfigOpen, setConsumerConfigOpen] = useState(false);

    const { invokeUrl, isLoading, isError } = useAgentEndpointResources({
        orgId, projectId, agentId, envId, external,
    });

    // Mirrors DeployCard.tsx's authMode derivation so the wording matches the
    // Deploy page's own security summary.
    const authMode: AuthMode = configurations?.enableOAuthSecurity
        ? "oauth"
        : configurations?.enableApiKeySecurity
            ? "apikey"
            : "none";
    const authHeaderPrefix = configurations?.oauthConfig?.authHeaderPrefix || "Bearer";
    const oauthHeaderName = configurations?.oauthConfig?.headerName || "Authorization";
    const { label: authLabel, tooltip: authTooltip, headerExample: authHeaderExample } =
        getAuthPresentation(authMode, authHeaderPrefix, oauthHeaderName);
    const oauthIssuers = configurations?.oauthConfig?.issuers ?? EMPTY_ISSUERS;

    const corsEnabled = configurations?.corsConfig?.enabled ?? false;
    const corsOrigins = configurations?.corsConfig?.allowOrigin ?? [];
    const corsAllOrigins = corsOrigins.includes("*");
    const corsLabel = corsEnabled
        ? `Enabled · ${corsAllOrigins ? "all origins" : "allow-listed origins"}`
        : "Disabled";
    const corsTooltip = corsEnabled
        ? corsAllOrigins ? "Any origin may call this endpoint" : corsOrigins.join(", ")
        : "Cross-origin browser requests are blocked";

    // Not applicable to external agents at all: they aren't deployed through
    // this platform, so there's nothing to fetch, and the disabled query never
    // settles isLoading. Static per instance, so bailing out here (rather than
    // routing through CollapsibleSection like the loading/empty cases below)
    // skips building the JSX for a section that can never show.
    if (external) {
        return null;
    }

    const deploymentPath = getAgentDeploymentPath(orgId, projectId, agentId);

    // Only worth showing once the environment is actually deployed and has a
    // resolved invoke URL — an inactive environment can still have a URL left
    // over from a prior deployment, and surfacing it as if it were live would
    // be misleading. A failed fetch is shown regardless, so the failure isn't
    // silently treated as "nothing deployed yet".
    const show = isError
        || (deploymentStatus === DeploymentStatus.ACTIVE && !isLoading && !!invokeUrl);

    return (
        <>
            <CollapsibleSection show={show}>
                {(invokeUrl || isError) && (
                    <OverviewSectionCard
                        title="API Endpoint"
                        actionHref={deploymentPath}
                        actionLabel="Deployments"
                        headerAction={(
                            <Tooltip title="Open the consumer configuration">
                                <Button
                                    size="small"
                                    variant="text"
                                    startIcon={<Plug size={14} />}
                                    onClick={() => setConsumerConfigOpen(true)}
                                    sx={{
                                        minWidth: 0,
                                        fontSize: (theme) => theme.typography.caption.fontSize,
                                    }}
                                >
                                    Connect
                                </Button>
                            </Tooltip>
                        )}
                        sx={{ mb: 1.5 }}
                    >
                        {isError ? (
                            <Typography variant="body2" color="error">
                                Unable to load the API endpoint. Try again later.
                            </Typography>
                        ) : (
                            <>
                                <TextInput
                                    label="Invoke URL"
                                    value={invokeUrl}
                                    copyable
                                    copyTooltipText="Copy URL"
                                    slotProps={{ input: { readOnly: true } }}
                                    sx={{ mb: 1 }}
                                />
                                <Box display="flex" flexWrap="wrap" gap={1}>
                                    <StatusPill
                                        label="Auth"
                                        value={authLabel}
                                        tooltip={authTooltip}
                                    />
                                    <StatusPill
                                        label="CORS"
                                        value={corsLabel}
                                        tooltip={corsTooltip}
                                    />
                                </Box>
                            </>
                        )}
                    </OverviewSectionCard>
                )}
            </CollapsibleSection>
            {/* Rendered outside CollapsibleSection so it isn't retained inside
                collapsed (zero-height) content — DrawerWrapper's underlying
                MUI Drawer portals to document.body regardless, so nesting it
                inside a collapsed ancestor wouldn't actually hide it. Gating
                `open` on `show` also closes it if the card itself disappears
                (e.g. the environment goes inactive) instead of leaving a
                stale drawer open with no visible trigger behind it. */}
            {invokeUrl && (
                <ConsumerConfigDrawer
                    open={consumerConfigOpen && show}
                    onClose={() => setConsumerConfigOpen(false)}
                    orgId={orgId}
                    projectId={projectId}
                    agentId={agentId}
                    envId={envId}
                    invokeUrl={invokeUrl}
                    authMode={authMode}
                    authLabel={authLabel}
                    authHeaderExample={authHeaderExample}
                    oauthIssuers={oauthIssuers}
                />
            )}
        </>
    );
};
