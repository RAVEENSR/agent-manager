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

/**
 * Sorts system-injected entries (isSystem=true) below user-managed ones,
 * preserving relative order within each group.
 */
export function sortSystemLast<T extends { isSystem?: boolean }>(items: T[]): T[] {
  return [...items].sort((a, b) => (a.isSystem ? 1 : 0) - (b.isSystem ? 1 : 0));
}

/**
 * Drops system-injected entries: they're platform-managed and re-applied by
 * the backend independently of any save/promote payload.
 */
export function excludeSystemVars<T extends { key: string; isSystem?: boolean }>(
  items: T[],
): T[] {
  return items.filter((item) => item.key && !item.isSystem);
}
