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

import { Box, Chip, Skeleton, Stack, Typography } from "@wso2/oxygen-ui";
import {
  useGetAgentGroups,
  useGetAgentRoles,
} from "@agent-management-platform/api-client";

const ChipGroup: React.FC<{
  label: string;
  items: { id: string; name: string }[];
  emptyText: string;
}> = ({ label, items, emptyText }) => (
  <Box>
    <Typography variant="body2" color="text.secondary" mb={0.5}>
      {label}
    </Typography>
    {items.length === 0 ? (
      <Typography variant="body2" color="text.disabled">
        {emptyText}
      </Typography>
    ) : (
      <Stack direction="row" flexWrap="wrap" gap={1}>
        {items.map((item) => (
          <Chip key={item.id} label={item.name} size="small" />
        ))}
      </Stack>
    )}
  </Box>
);

interface UseAgentRolesAndGroupsParams {
  orgId: string;
  projectId: string;
  agentId: string;
  envId: string;
  enabled: boolean;
}

/**
 * Fetches the agent identity's effective Thunder roles/groups for one
 * environment. `enabled` should reflect whether the binding has completed
 * provisioning — the backend 404s (ErrAgentIdentityNotProvisioned) otherwise.
 * Shared by the overview page's EnvAgentRolesGroupsSection and the Configure
 * Agent page's Manage AgentID drawer — both show the same roles/groups chips
 * for an agent+environment, just in different surrounding chrome.
 */
export function useAgentRolesAndGroups({
  orgId, projectId, agentId, envId, enabled,
}: UseAgentRolesAndGroupsParams) {
  const {
    data: rolesData,
    isLoading: isLoadingRoles,
    isError: isRolesError,
    error: rolesError,
  } = useGetAgentRoles(
    { orgName: orgId, projName: projectId, agentName: agentId },
    { environment: envId },
    { enabled },
  );
  const {
    data: groupsData,
    isLoading: isLoadingGroups,
    isError: isGroupsError,
    error: groupsError,
  } = useGetAgentGroups(
    { orgName: orgId, projName: projectId, agentName: agentId },
    { environment: envId },
    { enabled },
  );

  return {
    roles: rolesData?.roles ?? [],
    groups: groupsData?.groups ?? [],
    isLoading: isLoadingRoles || isLoadingGroups,
    isError: isRolesError || isGroupsError,
    error: rolesError ?? groupsError,
  };
}

/**
 * Roles + Groups chip lists, stacked.
 */
export const RolesGroupsChips: React.FC<{
  roles: { id: string; name: string }[];
  groups: { id: string; name: string }[];
  isLoading: boolean;
}> = ({ roles, groups, isLoading }) => (
  isLoading ? (
    <Stack spacing={1.5}>
      <Skeleton variant="rounded" height={32} />
      <Skeleton variant="rounded" height={32} />
    </Stack>
  ) : (
    <Stack spacing={1.5}>
      <ChipGroup label="Roles" items={roles} emptyText="No roles assigned." />
      <ChipGroup label="Groups" items={groups} emptyText="Not a member of any groups." />
    </Stack>
  )
);
