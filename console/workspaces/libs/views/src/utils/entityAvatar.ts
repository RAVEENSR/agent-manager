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

/** Palette used when no explicit color is given — picked by name hash so a
 * given entity keeps the same color across renders and across pages. */
const ENTITY_AVATAR_COLORS = [
  '#5B5FEE', '#0EA5E9', '#EC4899', '#F59E0B', '#10B981', '#8B5CF6',
];

function hashString(value: string): number {
  let hash = 0;
  for (let i = 0; i < value.length; i += 1) {
    hash = (hash * 31 + value.charCodeAt(i)) | 0;
  }
  return Math.abs(hash);
}

/** Deterministic avatar background color for an entity name. */
export function getEntityAvatarColor(name?: string): string {
  if (!name?.trim()) return ENTITY_AVATAR_COLORS[0];
  return ENTITY_AVATAR_COLORS[hashString(name.trim().toLowerCase()) % ENTITY_AVATAR_COLORS.length];
}

export interface AvatarInitialsOptions {
  fallback?: string;
  maxChars?: number;
}

/**
 * Leading letters of a name, for letter avatars. Non-letters are dropped so
 * handles like `1-my-agent` still yield a readable initial.
 */
export function getAvatarInitials(
  value: string | undefined,
  options: AvatarInitialsOptions = {},
): string {
  const { fallback = '??', maxChars = 2 } = options;
  if (!value) return fallback;
  const letters = value.replace(/[^A-Za-z]/g, '');
  if (!letters) return fallback;
  return letters.slice(0, Math.max(1, maxChars)).toUpperCase();
}
