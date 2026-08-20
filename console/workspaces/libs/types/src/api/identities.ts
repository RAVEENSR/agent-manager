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

import type { OrgPathParams, PaginationMeta } from './common';

// --- Users ---

export interface ThunderClaim {
  type: string;
  value: string;
}

export interface ThunderUser {
  id: string;
  type?: string;
  ouId?: string;
  attributes?: Record<string, unknown>;
  groups?: Array<{ id: string; name: string }>;
  createdAt?: string;
  updatedAt?: string;
}

export interface ThunderUserListResponse extends PaginationMeta {
  users: ThunderUser[];
}

export interface CreateUserRequest {
  type: string;
  attributes: Record<string, string>;
}

export interface UpdateUserRequest {
  attributes?: Record<string, string>;
}

export interface InviteUserRequest {
  email: string;
}

export interface InviteUserResponse {
  inviteLink: string;
}

// --- Groups ---

export interface ThunderGroup {
  id: string;
  ouId?: string;
  name: string;
  description?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface ThunderGroupListResponse extends PaginationMeta {
  groups: ThunderGroup[];
}

export interface CreateGroupRequest {
  name: string;
  description?: string;
}

export interface UpdateGroupRequest {
  name: string;
  description?: string;
}

// --- Roles ---

export interface ThunderRole {
  id: string;
  ouId?: string;
  name: string;
  description?: string;
  permissions?: RolePermissionRequest[];
  isReadOnly?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface ThunderRoleListResponse extends PaginationMeta {
  roles: ThunderRole[];
}

export interface ThunderRoleAssignments {
  permissions?: string[];
  users?: ThunderUser[];
  groups?: ThunderGroup[];
}

export interface CreateRoleRequest {
  name: string;
  description?: string;
}

export interface UpdateRoleRequest {
  name: string;
  description?: string;
}

export interface RolePermissionRequest {
  resourceServerId: string;
  permissions: string[];
}

export interface RoleUserGroupRequest {
  userIds?: string[];
  groupIds?: string[];
}

// --- Permissions catalog ---

export interface ThunderPermission {
  name: string;
  resourceServerId: string;
  actionName: string;
  resourceName: string;
}

export interface AMPPermissionsResponse {
  permissions: ThunderPermission[];
  resourceServerId: string;
}

// --- Path params ---

export type IdentityOrgPathParams = OrgPathParams;
export type UserPathParams = OrgPathParams & { userId: string };
export type GroupPathParams = OrgPathParams & { groupId: string };
export type RolePathParams = OrgPathParams & { roleId: string };
