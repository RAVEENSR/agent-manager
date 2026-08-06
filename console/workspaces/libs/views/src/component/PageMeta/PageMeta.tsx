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

import { Box, Stack, Tooltip, Typography, type SxProps, type Theme } from '@wso2/oxygen-ui';
import type { ReactNode } from 'react';

export interface PageMetaItemProps {
  /** Small (12–16px) icon standing in for the label. */
  icon?: ReactNode;
  /**
   * What the value is. Shown as the icon's tooltip so the rows stay compact,
   * or as "Label:" text when there is no icon to hang it off.
   */
  label?: string;
  /** Strings and numbers get the caption treatment; nodes (chips, links) render as-is. */
  children: ReactNode;
  sx?: SxProps<Theme>;
}

/**
 * One line of entity metadata — `<icon> value` — as used under a page title
 * (created at, owner, tags) and inside overview cards. Keeps the icon size,
 * tooltip and value weight consistent across all of them.
 */
export function PageMetaItem({ icon, label, children, sx }: PageMetaItemProps) {
  const isPlainValue = typeof children === 'string' || typeof children === 'number';

  const iconBox = icon && (
    <Box display="flex" alignItems="center" color="text.secondary" sx={{ flexShrink: 0 }}>
      {icon}
    </Box>
  );

  return (
    <Box display="flex" alignItems="center" gap={0.75} minWidth={0} sx={sx}>
      {/* Only pay for a Tooltip when there's actually a label to show in it. */}
      {label && iconBox ? <Tooltip title={label}>{iconBox}</Tooltip> : iconBox}
      {label && !icon && (
        <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
          {label}:
        </Typography>
      )}
      {isPlainValue ? (
        // Text values (created at, creator) stay low-emphasis so they read as
        // background detail; node values bring their own styling (e.g. chips).
        <Typography variant="caption" fontWeight={500} color="text.disabled" noWrap>
          {children}
        </Typography>
      ) : (
        children
      )}
    </Box>
  );
}

export interface PageMetaListProps {
  children: ReactNode;
  sx?: SxProps<Theme>;
}

/** Stacks {@link PageMetaItem} rows (and any other metadata nodes) under a title. */
export function PageMetaList({ children, sx }: PageMetaListProps) {
  return (
    <Stack spacing={0.5} sx={{ minWidth: 0, ...sx }}>
      {children}
    </Stack>
  );
}
