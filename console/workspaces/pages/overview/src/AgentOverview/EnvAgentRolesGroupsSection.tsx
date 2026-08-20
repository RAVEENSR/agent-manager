/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useMemo, useState } from "react";
import { Alert, Avatar, Box, Button, CircularProgress, IconButton, Skeleton, Tooltip, Typography } from "@wso2/oxygen-ui";
import { AlertTriangle, Copy, Fingerprint, RefreshCw } from "@wso2/oxygen-ui-icons-react";
import {
  useAgentIdentityBinding,
  useListAgentIdentityAgents,
  useRetryAgentIdentityProvisioning,
} from "@agent-management-platform/api-client";
import { isAgentIdentityEnabled } from "@agent-management-platform/types";
import {
  OverviewSectionCard,
  useAgentRolesAndGroups,
} from "@agent-management-platform/shared-component";
import { buildAgentIdHref } from "./agentIdLink";

interface EnvAgentRolesGroupsSectionProps {
  orgId: string;
  projectId: string;
  agentId: string;
  envId: string;
}

/**
 * Per-environment "Agent ID" card, laid out like an identity badge: an
 * identity avatar, the Thunder Agent ID as an inline copyable value, and the
 * identity's roles/groups as `Role:`/`Group:` tags on one line. Rendered
 * inside an EnvironmentCard for both internal and external agents — the client
 * ID/secret/regenerate flow lives on the agent-level "Agent ID" page instead,
 * linked to via the "View all" button. The Thunder Agent ID mirrors the one
 * shown on that page, resolved from the same useListAgentIdentityAgents
 * response.
 */
export const EnvAgentRolesGroupsSection: React.FC<EnvAgentRolesGroupsSectionProps> = ({
  orgId, projectId, agentId, envId,
}) => {
  // useAgentIdentityBinding has no `enabled` option, so the ids are withheld
  // when Agent ID is disabled to keep the identity request from firing.
  const agentIdEnabled = isAgentIdentityEnabled();
  const { binding, provisioned, isLoading: isLoadingIdentity } = useAgentIdentityBinding(
    agentIdEnabled
      ? { orgId, projectId, agentId, envId }
      : { orgId: "", projectId: "", agentId: "", envId: "" },
  );
  const isFailed = binding?.status === "failed";

  const { mutate: retryProvisioning, isPending: isRetrying } = useRetryAgentIdentityProvisioning();
  const handleRetry = () => {
    retryProvisioning({
      params: { orgName: orgId, projName: projectId, agentName: agentId },
      body: { environment: envId },
    });
  };

  const { roles, groups, isLoading, isError: isRolesGroupsError } = useAgentRolesAndGroups({
    orgId, projectId, agentId, envId, enabled: provisioned,
  });

  // Same lookup the agent-id page uses: the Thunder Agent ID is a field on the
  // per-env identity-agents list, matched by agent + project name.
  const { data: identityAgentsData, isError: isAgentsError } = useListAgentIdentityAgents(
    agentIdEnabled ? { orgName: orgId, envName: envId } : { orgName: "", envName: "" },
  );
  const thunderAgentId = useMemo(
    () => identityAgentsData?.agents.find(
      (item) => item.agentName === agentId && item.projectName === projectId,
    )?.thunderAgentId,
    [identityAgentsData, agentId, projectId],
  );

  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    if (!thunderAgentId) return;
    void navigator.clipboard.writeText(thunderAgentId);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  const hasTags = roles.length > 0 || groups.length > 0;

  if (!agentIdEnabled) return null;

  return (
    <OverviewSectionCard
      title="Agent ID"
      actionHref={buildAgentIdHref(orgId, projectId, agentId, envId)}
      sx={{ height: "100%" }}
    >
      <Box display="flex" gap={2} alignItems="center">
        <Avatar
          variant="rounded"
          sx={{
            width: 48,
            height: 48,
            flexShrink: 0,
            bgcolor: "primary.main",
            color: "primary.contrastText",
          }}
        >
          <Fingerprint size={24} />
        </Avatar>
        {isLoadingIdentity ? (
          <Skeleton variant="text" width={160} height={20} />
        ) : isFailed ? (
          <Typography variant="body2" color="error" fontWeight={600}>
            Provisioning Status : Failed
          </Typography>
        ) : (
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {thunderAgentId ? (
            <Box display="flex" alignItems="center" gap={0.5} minWidth={0}>
              <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
                Agent ID:
              </Typography>
              <Typography
                variant="body2"
                color="text.secondary"
                noWrap
                sx={{ fontFamily: "monospace" }}
              >
                {thunderAgentId}
              </Typography>
              <Tooltip title={copied ? "Copied" : "Copy Agent ID"}>
                <IconButton size="small" onClick={handleCopy} sx={{ p: 0.25, flexShrink: 0 }}>
                  <Copy size={14} />
                </IconButton>
              </Tooltip>
            </Box>
          ) : isAgentsError ? (
            <Typography variant="body2" color="error">
              Unable to load Agent ID. Try again later.
            </Typography>
          ) : provisioned ? (
            <Typography variant="body2" color="text.disabled">
              Provisioning identity…
            </Typography>
          ) : (
            <Typography variant="body2" color="text.disabled">
              Agent ID not available
            </Typography>
          )}
          <Box mt={0.5}>
            {isLoading ? (
              <Skeleton variant="text" width={180} height={16} />
            ) : isRolesGroupsError ? (
              <Typography variant="caption" color="error">
                Unable to load roles/groups. Try again later.
              </Typography>
            ) : hasTags ? (
              <>
                {roles.length > 0 && (
                  <Typography variant="caption" color="text.disabled" display="block">
                    Roles: {roles.map((role) => role.name).join(", ")}
                  </Typography>
                )}
                {groups.length > 0 && (
                  <Typography variant="caption" color="text.disabled" display="block">
                    Groups: {groups.map((group) => group.name).join(", ")}
                  </Typography>
                )}
              </>
            ) : (
              <Typography variant="caption" color="text.disabled">
                No roles or groups assigned
              </Typography>
            )}
          </Box>
        </Box>
        )}
      </Box>
      {isFailed && !isLoadingIdentity && (
        <Alert
          severity="error"
          icon={<AlertTriangle size={18} />}
          action={
            <Button
              color="inherit"
              size="small"
              onClick={handleRetry}
              disabled={isRetrying}
              startIcon={isRetrying ? <CircularProgress size={14} color="inherit" /> : <RefreshCw size={14} />}
              sx={{ whiteSpace: "nowrap" }}
            >
              {isRetrying ? "Retrying..." : "Retry"}
            </Button>
          }
          sx={{ mt: 1.5, flexWrap: "wrap", "& .MuiAlert-action": { flexShrink: 0 } }}
        >
          Provisioning failed{binding?.lastError ? `: ${binding.lastError}` : ""}
        </Alert>
      )}
    </OverviewSectionCard>
  );
};
