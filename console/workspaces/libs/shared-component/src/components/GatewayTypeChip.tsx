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

import { Chip } from "@wso2/oxygen-ui";
import type { GatewayType } from "@agent-management-platform/types";

interface GatewayTypeChipProps {
  type: GatewayType | string;
}

const GATEWAY_TYPE_LABELS: Record<string, string> = {
  INGRESS: "Ingress",
  EGRESS: "Egress",
  BOTH: "Ingress + Egress",
};

export function GatewayTypeChip({ type }: GatewayTypeChipProps) {
  // Server emits canonical uppercase (see gatewayType normalization); .toUpperCase()
  // is belt-and-braces for old cached responses.
  const normalized = type.toUpperCase();
  const label = GATEWAY_TYPE_LABELS[normalized] ?? normalized;
  const isEgressCapable = normalized === "EGRESS" || normalized === "BOTH";

  return (
    <Chip
      label={label}
      size="small"
      variant="outlined"
      sx={(theme) =>
        isEgressCapable
          ? {
              color: theme.vars?.palette.info.main ?? theme.palette.info.main,
              bgcolor: `rgba(${theme.vars?.palette.info.mainChannel ?? "0 0 0"} / 0.08)`,
              borderColor: `rgba(${theme.vars?.palette.info.mainChannel ?? "0 0 0"} / 0.3)`,
            }
          : {
              color: theme.vars?.palette.text.secondary ?? theme.palette.text.secondary,
            }
      }
    />
  );
}
