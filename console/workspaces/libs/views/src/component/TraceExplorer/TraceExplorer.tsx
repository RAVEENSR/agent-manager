/**
 * Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com).
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

import type { Span, LLMData, AgentData } from '@agent-management-platform/types';
import {
  Box,
  ButtonBase,
  Chip,
  Collapse,
  IconButton,
  Stack,
  Tooltip,
  Typography,
  Alert,
} from '@wso2/oxygen-ui';
import { memo, useCallback, useMemo, useState } from 'react';
import {
  Clock,
  Brain,
  ChevronDown,
  Minus,
  XCircle,
  Link,
  Coins,
  CircleQuestionMark,
  Wrench,
  Layers,
  Search,
  ArrowUpDown,
  Bot,
  ClipboardCheck,
  Info,
} from '@wso2/oxygen-ui-icons-react';

interface TraceExplorerProps {
  spans: Span[];
  onOpenAttributesClick: (span: Span) => void;
  selectedSpan: Span | null;
}

interface RenderSpan {
  span: Span;
  key: string;
  parentKey: string | null;
  childrenKeys: string[] | null;
}

// Helper function to extract token usage from data based on span kind
function getTokenUsage(span: Span) {
  const { kind, data } = span.ampAttributes || {};
  if (kind === 'llm' && data) {
    return (data as LLMData).tokenUsage;
  } else if (kind === 'agent' && data) {
    return (data as AgentData).tokenUsage;
  }
  return undefined;
}

export function SpanIcon({ span }: { span: Span }) {
  const kind = span.ampAttributes?.kind;

  switch (kind) {
    case 'llm':
      return (
        <Box color="primary.main">
          <Brain size={16} />
        </Box>
      );
    case 'embedding':
      return (
        <Box color="success.main">
          <Layers size={16} />
        </Box>
      );
    case 'tool':
      return (
        <Box color="info.light">
          <Wrench size={16} />
        </Box>
      );
    case 'retriever':
      return (
        <Box color="info.main">
          <Search size={16} />
        </Box>
      );
    case 'rerank':
      return (
        <Box color="success.main">
          <ArrowUpDown size={16} />
        </Box>
      );
    case 'agent':
      return (
        <Box color="warning.main">
          <Bot size={16} />
        </Box>
      );
    case 'crewaitask':
      return (
        <Box sx={{ color: '#9C27B0' }}>
          <ClipboardCheck size={16} />
        </Box>
      );
    case 'chain':
      return (
        <Box color="text.secondary">
          <Link size={16} />
        </Box>
      );
    default:
      return (
        <Box color="secondary.dark">
          <CircleQuestionMark size={16} />
        </Box>
      );
  }
}

function formatDuration(durationInNanos: number) {
  if (durationInNanos > 1000 * 1000 * 1000) {
    return `${(durationInNanos / (1000 * 1000 * 1000)).toFixed(2)}s`;
  }
  if (durationInNanos > 1000 * 1000) {
    return `${(durationInNanos / (1000 * 1000)).toFixed(2)}ms`;
  }
  return `${(durationInNanos / 1000).toFixed(2)}μs`;
}
const populateRenderSpans = (
  spans: Span[]
): {
  spanMap: Map<string, RenderSpan>;
  rootSpans: string[];
} => {
  // Sort spans by start time (earliest first)
  const sortedSpans = [...spans].sort((a, b) => {
    const timeA = new Date(a.startTime).getTime();
    const timeB = new Date(b.startTime).getTime();
    return timeA - timeB;
  });

  // First pass: Build a map of spanId -> array of child spanIds
  const childrenMap = new Map<string, string[]>();
  const rootSpans: string[] = [];
  const spanKeySet = new Set<string>(sortedSpans.map((span) => span.spanId));

  sortedSpans.forEach((span) => {
    // Make it considered as a parent span if parent span is not there in the sorted spans
    // or parent span is null
    if (span.parentSpanId && spanKeySet.has(span.parentSpanId)) {
      const children = childrenMap.get(span.parentSpanId) || [];
      children.push(span.spanId);
      childrenMap.set(span.parentSpanId, children);
    } else {
      rootSpans.push(span.spanId);
    }
  });

  // Second pass: Create RenderSpan objects and store them in a Map keyed by spanId
  const spanMap = new Map<string, RenderSpan>();

  sortedSpans.forEach((span) => {
    const childrenKeys = childrenMap.get(span.spanId) || null;
    spanMap.set(span.spanId, {
      span,
      key: span.spanId,
      parentKey: span.parentSpanId || null,
      childrenKeys: childrenKeys,
    });
  });

  return { spanMap, rootSpans };
};

// Below this span count, every row starts expanded (matches the pre-existing
// UX for the common case). Above it, only root spans start expanded so a
// large trace doesn't mount hundreds of rows at once.
const EXPAND_ALL_THRESHOLD = 50;

interface SpanRowProps {
  node: RenderSpan;
  spanMap: Map<string, RenderSpan>;
  onOpenAttributesClick: (span: Span) => void;
  selectedSpanId: string | null;
  expandedKeys: Set<string>;
  onToggleExpanded: (key: string) => void;
  isLastChild?: boolean;
  isRoot?: boolean;
}

// Expand/collapse state is lifted to TraceExplorer (rather than kept as local
// state here) so it survives a Collapse's unmountOnExit remounting this row
// when an ancestor is collapsed and re-expanded.
const SpanRow = memo(function SpanRow({
  node,
  spanMap,
  onOpenAttributesClick,
  selectedSpanId,
  expandedKeys,
  onToggleExpanded,
  isLastChild,
  isRoot,
}: SpanRowProps) {
  const expanded = expandedKeys.has(node.key);
  const isSelected = selectedSpanId === node.key;
  const childCount = node.childrenKeys?.length ?? 0;
  const hasChildren = childCount > 0;

  return (
    <Stack spacing={1} width="100%">
      {/* Connecting lines - only show for non-root nodes */}
      {!isRoot && (
        <>
          {/* Horizontal line */}
          <Box
            position="absolute"
            sx={{
              width: 32,
              height: 40,
              borderLeft: isLastChild ? `2px solid` : 'none',
              borderBottom: `2px solid`,
              borderColor: 'divider',
              left: -32,
              top: -14,
              borderBottomLeftRadius: isLastChild ? 8 : 0,
            }}
          />
          {/* Vertical line continuing down (only if not last child) */}
          {!isLastChild && (
            <Box
              position="absolute"
              sx={{
                width: 1,
                height: '100%',
                borderLeft: `2px solid`,
                borderColor: 'divider',
                left: -32,
                top: -20,
              }}
            />
          )}
        </>
      )}
      <ButtonBase
        onClick={() => onOpenAttributesClick(node.span)}
        sx={{
          width: '100%',
        }}
      >
        <Stack
          direction="row"
          width="100%"
          justifyContent="space-between"
          sx={{
            border: `1px solid`,
            borderColor: isSelected ? 'primary.main' : 'divider',
            borderRadius: 0.5,
            backgroundColor: 'background.paper',
            px: 1,
            transition: 'all 0.2s ease-in-out',
            '&:hover': {
              backgroundColor: 'background.default',
            },
          }}
        >
          <Stack
            direction="row"
            spacing={1}
            flexGrow={1}
            alignItems="center"
            maxWidth="100%"
          >
            <IconButton
              disabled={!hasChildren}
              onClick={(e) => {
                e.stopPropagation();
                e.preventDefault();
                onToggleExpanded(node.key);
              }}
              size="small"
              color="primary"
            >
              {hasChildren ? (
                <>
                  <Box
                    component="span"
                    sx={{
                      transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
                      display: 'inline-flex',
                      transition: 'transform 0.2s ease-in-out',
                    }}
                  >
                    <ChevronDown size={16} />
                  </Box>
                </>
              ) : (
                <Minus size={16} />
              )}
            </IconButton>
            <SpanIcon span={node.span} />
            <Stack
              direction="column"
              p={0.5}
              alignItems="start"
              overflow="hidden"
            >
              <Stack
                direction="row"
                spacing={1}
                alignItems="center"
                maxWidth="100%"
              >
                <Tooltip
                  title={node.span.name}
                  disableHoverListener={node.span.name.length < 30}
                >
                  <Typography
                    variant="body2"
                    noWrap
                    textOverflow="ellipsis"
                    maxWidth="70%"
                    overflow="hidden"
                  >
                    {node.span.name}
                  </Typography>
                </Tooltip>
                {node.span.ampAttributes?.status?.error && (
                  <Stack justifyContent="center" sx={{ color: 'error.main' }}>
                    <XCircle size={16} />
                  </Stack>
                )}
                <Chip
                  icon={<Clock size={16} />}
                  label={formatDuration(node.span.durationInNanos)}
                  size="small"
                  variant="outlined"
                />
                {(() => {
                  const tokenUsage = getTokenUsage(node.span);
                  return (
                    tokenUsage && (
                      <Tooltip
                        title={`${tokenUsage.inputTokens} input tokens, ${tokenUsage.outputTokens} output tokens`}
                      >
                        <Chip
                          icon={<Coins size={16} />}
                          label={tokenUsage.totalTokens}
                          size="small"
                          variant="outlined"
                        />
                      </Tooltip>
                    )
                  );
                })()}
              </Stack>
            </Stack>
          </Stack>
          <Stack direction="row" spacing={1} alignItems="center">
            {isRoot && node.parentKey && (
              <Tooltip title="Unable to determine the parent span">
                <Box color="warning.main">
                  <Info size={16} />
                </Box>
              </Tooltip>
            )}
          </Stack>
        </Stack>
      </ButtonBase>
      {hasChildren && (
        <Collapse in={expanded} unmountOnExit>
          <Box display="flex" flexDirection="column" pl={4} position="relative">
            {node.childrenKeys?.map((childKey, index) => {
              const childNode = spanMap.get(childKey);
              if (!childNode) return null;
              return (
                <Box key={childKey} display="flex" position="relative">
                  <SpanRow
                    node={childNode}
                    spanMap={spanMap}
                    onOpenAttributesClick={onOpenAttributesClick}
                    selectedSpanId={selectedSpanId}
                    expandedKeys={expandedKeys}
                    onToggleExpanded={onToggleExpanded}
                    isLastChild={index === childCount - 1}
                    isRoot={false}
                  />
                </Box>
              );
            })}
          </Box>
        </Collapse>
      )}
    </Stack>
  );
});

export function TraceExplorer(props: TraceExplorerProps) {
  const { spans, onOpenAttributesClick, selectedSpan } = props;
  const selectedSpanId = selectedSpan?.spanId ?? null;

  const renderingSpans = useMemo(() => populateRenderSpans(spans), [spans]);

  // Expanded keys live for the lifetime of this TraceExplorer instance (reset
  // only when it's given a different trace's spans, i.e. remounted), so
  // collapsing/re-expanding an ancestor doesn't discard descendants' choices.
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(() =>
    spans.length <= EXPAND_ALL_THRESHOLD
      ? new Set(spans.map((span) => span.spanId))
      : new Set(renderingSpans.rootSpans)
  );

  const toggleExpanded = useCallback((key: string) => {
    setExpandedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);

  return (
    <Stack direction="column" spacing={2}>
      {renderingSpans.rootSpans.length > 1 && (
        <Alert severity="warning" sx={{ mb: 1 }}>
          Some trace details are missing or incomplete.
        </Alert>
      )}
      {renderingSpans.rootSpans.map((rootKey, index) => {
        const rootNode = renderingSpans.spanMap.get(rootKey);
        if (!rootNode) return null;
        return (
          <Stack key={rootKey}>
            <SpanRow
              node={rootNode}
              spanMap={renderingSpans.spanMap}
              onOpenAttributesClick={onOpenAttributesClick}
              selectedSpanId={selectedSpanId}
              expandedKeys={expandedKeys}
              onToggleExpanded={toggleExpanded}
              isLastChild={index === renderingSpans.rootSpans.length - 1}
              isRoot
            />
          </Stack>
        );
      })}
    </Stack>
  );
}
