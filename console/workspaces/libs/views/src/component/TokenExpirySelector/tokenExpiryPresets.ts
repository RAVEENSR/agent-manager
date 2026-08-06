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

export interface TokenExpiryPreset {
  label: string;
  value: string; // Go duration string, e.g. "2160h"
}

// Tracing token expiry presets shared by the external-agent generation flow and the
// internal-agent regenerate control. Values are Go duration strings the backend parses directly.
export const TOKEN_EXPIRY_PRESETS: TokenExpiryPreset[] = [
  { label: "30 days", value: "720h" },
  { label: "60 days", value: "1440h" },
  { label: "90 days", value: "2160h" },
];

export const DEFAULT_TOKEN_EXPIRY = "2160h"; // 90 days

// Backend parseExpiryDuration caps expiry at 10 years.
const MAX_EXPIRY_HOURS = 10 * 365 * 24;

/**
 * Converts a future date into a Go duration string (whole hours from now) for the API's
 * `expiresIn` param. Throws when the date is not in the future or exceeds the backend's 10-year
 * cap, so callers can surface a validation error rather than relying on a 400 response.
 */
export const customDateToExpiresIn = (date: Date, now: Date = new Date()): string => {
  const diffMs = date.getTime() - now.getTime();
  if (diffMs <= 0) {
    throw new Error("Expiry date must be in the future");
  }
  const hours = Math.ceil(diffMs / (60 * 60 * 1000));
  if (hours > MAX_EXPIRY_HOURS) {
    throw new Error("Expiry cannot be more than 10 years from now");
  }
  return `${hours}h`;
};
