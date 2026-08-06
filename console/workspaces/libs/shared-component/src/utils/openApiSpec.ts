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
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import yaml from "js-yaml";

export function parseOpenApiSpecContent(
  content: string | undefined,
): Record<string, unknown> | undefined {
  if (!content?.trim()) return undefined;
  try {
    const spec = JSON.parse(content) as Record<string, unknown>;
    return spec && typeof spec === "object" ? spec : undefined;
  } catch {
    try {
      const spec = yaml.load(content) as Record<string, unknown>;
      return spec && typeof spec === "object" ? spec : undefined;
    } catch {
      return undefined;
    }
  }
}

const HTTP_METHODS = new Set(["get", "post", "put", "delete", "patch", "head", "options", "trace"]);

export interface OpenApiResource {
  method: string;
  path: string;
  summary?: string;
}

/** Flattens an OpenAPI spec's `paths` object into method+path pairs, sorted by path then method. */
export function extractOpenApiResources(
  spec: Record<string, unknown> | undefined,
): OpenApiResource[] {
  const paths = spec?.paths as Record<string, unknown> | undefined;
  if (!paths || typeof paths !== "object") return [];

  const resources: OpenApiResource[] = [];
  for (const path of Object.keys(paths)) {
    const operations = paths[path] as Record<string, unknown> | undefined;
    if (!operations || typeof operations !== "object") continue;

    for (const methodKey of Object.keys(operations)) {
      if (!HTTP_METHODS.has(methodKey.toLowerCase())) continue;
      const op = (operations[methodKey] || {}) as Record<string, unknown>;
      resources.push({
        method: methodKey.toUpperCase(),
        path,
        summary: (op?.summary ?? op?.description) as string | undefined,
      });
    }
  }

  resources.sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method));
  return resources;
}
