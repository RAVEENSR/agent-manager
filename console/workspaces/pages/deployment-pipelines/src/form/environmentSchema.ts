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

import { z } from "zod";

export const editEnvironmentSchema = z.object({
  displayName: z.string().min(1, "Display name is required").max(128, "Display name must be 128 characters or less"),
  description: z.string().nullable().optional(),
  isProduction: z.boolean().optional(),
});

export type EditEnvironmentFormValues = z.infer<typeof editEnvironmentSchema>;

export const isolationTiers = ["runc", "gvisor", "kata"] as const;

export type IsolationTier = (typeof isolationTiers)[number];

// Matches the DNS-label pattern agent-manager-service validates thunderHandle
// against (services/environment_service.go's thunderHandlePattern) and the 63-char
// DNS label limit ThunderIssuerURL itself enforces. Optional: omitting it lets
// agent-manager-service generate a 10-character handle instead (see
// add-environment-thunder.sh's THUNDER_HANDLE / register_thunder_url).
const thunderHandlePattern = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;

// Mirrors agent-manager-service's reservedThunderHandles (services/environment_service.go)
// exactly — labels that identify a real platform component/namespace, so allowing
// a handle to equal one risks hijacking or confusion once that component sits at
// the same hostname level (<handle>.<baseDomain>). Keep both lists in sync; the
// backend is still the source of truth and rejects these independently of this
// client-side check.
const RESTRICTED_THUNDER_HANDLES = new Set([
  "kubernetes",
  "kube-system",
  "kube-public",
  "kube-node-lease",
  "openchoreo",
  "opensearch",
  "prometheus",
  "otel-collector",
  "fluent-bit",
  "agent-manager",
  "observability",
]);

export const createEnvironmentSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .max(64, "Name must be 64 characters or less")
    .regex(/^[a-z0-9-]+$/, "Name must be lowercase alphanumeric with hyphens only"),
  displayName: z.string().min(1, "Display name is required").max(128, "Display name must be 128 characters or less"),
  description: z.string().optional(),
  dataplaneRef: z.string().min(1, "Data plane is required"),
  dnsPrefix: z.string().min(1, "DNS prefix is required").max(100),
  isProduction: z.boolean().optional(),
  isolationTier: z.enum(isolationTiers).optional(),
  thunderHandle: z
    .string()
    // Matches agent-manager-service's own minThunderHandleLen — a handle shorter
    // than what AMS would generate itself is trivially brute-forceable and defeats
    // the point of the feature, so this is a hard floor, not just advice.
    .min(10, "Handle must be at least 10 characters")
    .max(63, "Handle must be 63 characters or less")
    .regex(thunderHandlePattern, "Handle must be lowercase alphanumeric with hyphens only, no leading/trailing hyphen")
    .refine((value) => !RESTRICTED_THUNDER_HANDLES.has(value), {
      message: "This name is reserved for a platform component and can't be used as a handle",
    })
    .optional()
    .or(z.literal("")),
});

export type CreateEnvironmentFormValues = z.infer<typeof createEnvironmentSchema>;
