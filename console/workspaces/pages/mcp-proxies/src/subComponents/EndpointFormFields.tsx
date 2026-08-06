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

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { debounce } from "lodash";
import {
  useFetchMCPProxyServerInfo,
  useListGateways,
} from "@agent-management-platform/api-client";
import {
  type Environment,
  type GatewayResponse,
  type MCPServerInfoFetchResponse,
} from "@agent-management-platform/types";
import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Autocomplete,
  Box,
  Button,
  Chip,
  CircularProgress,
  Collapse,
  Divider,
  Form,
  FormControl,
  FormLabel,
  MenuItem,
  Select,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@wso2/oxygen-ui";
import { ChevronDown, HelpCircle } from "@wso2/oxygen-ui-icons-react";
import { useSnackBar } from "@agent-management-platform/views";
import { validateEndpointUrl } from "@agent-management-platform/shared-component";
import { MCPCapabilitiesView } from "../components/MCPCapabilitiesView";
import { AuthHeaderRow } from "./AuthHeaderRow";

// EndpointDraft is a single endpoint captured in the form. Its `id` maps to the backend
// endpoint handle (unique within the parent proxy); a fresh draft carries a temporary
// client id that the save path replaces with a handle derived from `name`/URL.
export interface EndpointDraft {
  id: string;
  // Human-readable endpoint name; the backend handle is derived from it when empty.
  name: string;
  url: string;
  authHeader: string;
  authValue: string;
  // Environment UUIDs (not names) this endpoint serves — stable across renames.
  environments: string[];
  // Egress gateway chosen per environment UUID, for environments with 2+
  // egress-capable gateways (a single candidate is inferred server-side, so it's
  // omitted here). Once a binding is deployed, its entry is the deployed gateway
  // and is locked; entries for undeployed bindings are the user's in-form pick.
  gatewayByEnv?: Record<string, string>;
  fetchedInfo: MCPServerInfoFetchResponse;
  serverName?: string;
  serverVersion?: string;
}

// Sentinel shown in place of a stored auth value (never returned by the API). While
// this is present untouched, the endpoint keeps its existing credential.
const MASKED_CREDENTIAL_VALUE = "••••••••••••";

// How long to wait after the last URL/auth edit before auto-fetching server info,
// so a fetch isn't fired on every keystroke.
const FETCH_DEBOUNCE_MS = 600;

export interface EndpointFormFieldsProps {
  orgId: string;
  // Environments not yet claimed by another endpoint. One environment can be used once.
  // In edit mode this must also include the edited endpoint's own environments.
  availableEnvironments: Environment[];
  onAdd: (endpoint: Omit<EndpointDraft, "id">) => void;
  onCancel: () => void;
  // When provided, the form edits an existing endpoint: fields are pre-filled and
  // its stored (unreadable) credential is masked until the user replaces it.
  initialDraft?: EndpointDraft;
}

export function EndpointFormFields({
  orgId,
  availableEnvironments,
  onAdd,
  onCancel,
  initialDraft,
}: EndpointFormFieldsProps) {
  const fetchServerInfo = useFetchMCPProxyServerInfo();
  const { pushSnackBar } = useSnackBar();

  const isEditing = Boolean(initialDraft);
  const hasStoredCredential = Boolean(initialDraft?.authHeader);

  const [name, setName] = useState(initialDraft?.name ?? "");
  const [url, setUrl] = useState(initialDraft?.url ?? "");
  const [authHeader, setAuthHeader] = useState(initialDraft?.authHeader ?? "");
  const [authValue, setAuthValue] = useState(
    hasStoredCredential ? MASKED_CREDENTIAL_VALUE : "",
  );
  const [isCredentialMasked, setIsCredentialMasked] =
    useState(hasStoredCredential);
  const [showAuthValue, setShowAuthValue] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(hasStoredCredential);
  // Mirrors Postman's per-header checkbox: unchecking excludes the header from the
  // fetch/save without losing the typed key/value.
  const [authEnabled, setAuthEnabled] = useState(true);
  const [selectedEnvIds, setSelectedEnvIds] = useState<string[]>(
    initialDraft?.environments ?? [],
  );
  // Gateway ids the endpoint is already deployed to, per environment — read once
  // from initialDraft (GET only echoes gatewayId for a binding that's actually
  // deployed), so this never changes for the life of the form and locks the select.
  const deployedGatewayByEnv = useMemo(
    () => initialDraft?.gatewayByEnv ?? {},
    [initialDraft],
  );
  // User's in-form gateway pick per environment, for bindings not yet deployed.
  const [selectedGatewayByEnv, setSelectedGatewayByEnv] = useState<
    Record<string, string>
  >({});
  const [fetchedInfo, setFetchedInfo] =
    useState<MCPServerInfoFetchResponse | null>(
      initialDraft?.fetchedInfo ?? null,
    );
  const [urlError, setUrlError] = useState<string | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);

  // When exactly one environment is available, there's nothing to choose between —
  // assign it automatically instead of showing a single-option selector.
  useEffect(() => {
    const onlyEnvId =
      availableEnvironments.length === 1 ? availableEnvironments[0].id : null;
    if (!onlyEnvId) return;
    setSelectedEnvIds((prev) => (prev.length === 0 ? [onlyEnvId] : prev));
  }, [availableEnvironments]);

  const { data: gatewaysData } = useListGateways({ orgName: orgId });

  // Egress-capable gateways (EGRESS or BOTH), grouped by the environment UUIDs
  // they're attached to. An environment with 0-1 candidates needs no picker: the
  // server infers the sole candidate, or there's genuinely nothing to deploy to.
  const egressGatewaysByEnv = useMemo(() => {
    const map: Record<string, GatewayResponse[]> = {};
    (gatewaysData?.gateways ?? []).forEach((gateway) => {
      if (gateway.gatewayType !== "EGRESS" && gateway.gatewayType !== "BOTH") {
        return;
      }
      (gateway.environments ?? []).forEach((env) => {
        if (!env.id) return;
        (map[env.id] ??= []).push(gateway);
      });
    });
    return map;
  }, [gatewaysData]);

  const environmentNameById = useMemo(() => {
    const map: Record<string, string> = {};
    availableEnvironments.forEach((env) => {
      if (env.id) map[env.id] = env.displayName || env.name;
    });
    return map;
  }, [availableEnvironments]);

  // Per-environment gateway actually sent on save: the locked deployed gateway
  // where one exists, otherwise the user's in-form pick. Environments left
  // unset are simply absent, so the payload omits gatewayId for them.
  const effectiveGatewayByEnv = useMemo(() => {
    const map: Record<string, string> = {};
    selectedEnvIds.forEach((envId) => {
      const value = deployedGatewayByEnv[envId] ?? selectedGatewayByEnv[envId];
      if (value) map[envId] = value;
    });
    return map;
  }, [selectedEnvIds, deployedGatewayByEnv, selectedGatewayByEnv]);

  const trimmedUrl = url.trim();
  const isFetched = Boolean(fetchedInfo);
  const isFetching = fetchServerInfo.isPending;
  const canAdd = isFetched && selectedEnvIds.length > 0;
  // Unchecking the header excludes it from the fetch/save entirely, same as leaving
  // it blank. An untouched masked credential means "keep the stored value", so it
  // resolves to empty (omitted) rather than replaying the sentinel to the backend.
  const effectiveAuthHeader = authEnabled ? authHeader.trim() : "";
  const effectiveAuthValue = authEnabled ? authValue.trim() : "";
  const resolvedAuthValue = isCredentialMasked ? "" : effectiveAuthValue;
  // In edit mode, Save stays disabled until the user actually changes something; a
  // brand-new endpoint (no initialDraft) is always a change. The credential counts as
  // changed only once the masked stored value has been replaced, and a re-fetch counts
  // when it returns different capabilities (e.g. newly added tools).
  const hasChanges =
    !initialDraft ||
    name.trim() !== initialDraft.name ||
    trimmedUrl !== initialDraft.url ||
    effectiveAuthHeader !== initialDraft.authHeader ||
    Boolean(resolvedAuthValue) ||
    !sameIdSet(selectedEnvIds, initialDraft.environments) ||
    !sameGatewayMap(effectiveGatewayByEnv, initialDraft.gatewayByEnv ?? {}) ||
    capabilitiesChanged(fetchedInfo, initialDraft.fetchedInfo);
  // An environment with 2+ egress candidates and no pick would be rejected by the
  // server as ambiguous, so require a selection wherever the picker renders.
  const hasGatewaySelection = selectedEnvIds.every((envId) => {
    const candidates = egressGatewaysByEnv[envId] ?? [];
    return (
      candidates.length <= 1 ||
      Boolean(deployedGatewayByEnv[envId] ?? selectedGatewayByEnv[envId])
    );
  });
  const canSave = canAdd && hasChanges && hasGatewaySelection;

  const clearFetched = useCallback(() => {
    setFetchedInfo(null);
  }, []);

  // Identifies the latest performFetch call whose network request should be allowed
  // to update state. A slow request can still be in flight when the debounced URL/auth
  // change fires the next one; without this, whichever resolves last wins, which isn't
  // necessarily the latest input.
  const fetchGenerationRef = useRef(0);

  const performFetch = useCallback(async () => {
    const urlValidationError = validateEndpointUrl(trimmedUrl, {
      requiredMessage: "Enter a valid MCP Server endpoint URL.",
      invalidMessage: "Enter a valid MCP Server endpoint URL.",
      protocolMessage: "Enter a valid MCP Server endpoint URL.",
    });
    if (urlValidationError) {
      setUrlError(urlValidationError);
      return;
    }
    setUrlError(null);

    // The stored credential is never returned, so it can't be replayed to the
    // live fetch — ask the user to re-enter it before re-fetching.
    if (effectiveAuthHeader && isCredentialMasked) {
      setIsCredentialMasked(false);
      setAuthValue("");
      setAdvancedOpen(true);
      setAuthError(
        "Re-enter the authentication value to re-fetch server info.",
      );
      return;
    }
    if (Boolean(effectiveAuthHeader) !== Boolean(effectiveAuthValue)) {
      setAdvancedOpen(true);
      setAuthError("Enter both an authentication header and value.");
      return;
    }
    setAuthError(null);

    const body =
      effectiveAuthHeader && effectiveAuthValue
        ? {
            url: trimmedUrl,
            auth: {
              type: "api-key" as const,
              header: effectiveAuthHeader,
              value: effectiveAuthValue,
            },
          }
        : { url: trimmedUrl };

    const generation = ++fetchGenerationRef.current;
    try {
      const result = await fetchServerInfo.mutateAsync({
        params: { orgName: orgId },
        body,
      });
      // A newer fetch was started while this one was in flight — its result (or
      // the newer one still pending) takes precedence, so drop this stale one.
      if (generation !== fetchGenerationRef.current) return;
      setFetchedInfo(result);
    } catch (err: unknown) {
      if (generation !== fetchGenerationRef.current) return;
      setFetchedInfo(null);
      if (
        typeof err === "object" &&
        err !== null &&
        (err as { code?: string }).code === "UNAUTHORIZED"
      ) {
        setAdvancedOpen(true);
        setAuthError(
          "This server requires authentication. Enter the credentials above.",
        );
      } else {
        const message =
          err instanceof Error
            ? err.message
            : "Failed to fetch MCP server info. Please check the URL and try again.";
        pushSnackBar({ message, type: "error" });
      }
    }
  }, [
    effectiveAuthHeader,
    effectiveAuthValue,
    isCredentialMasked,
    fetchServerInfo,
    orgId,
    pushSnackBar,
    trimmedUrl,
  ]);

  // Auto-fetch server info shortly after the URL or auth fields settle, instead of
  // requiring an explicit "Fetch" click. `performFetch` is re-created on every render
  // in which `fetchServerInfo` (the mutation object) changes identity — which react-query
  // does on its own pending/success/error transitions, not just when the form's inputs
  // change. Triggering off `performFetch` directly would re-schedule the debounce after
  // every fetch completes, causing a fetch loop. A ref sidesteps that: the effect that
  // schedules the debounce only depends on the actual field values, while the debounced
  // call always reads the latest `performFetch` out of the ref.
  const performFetchRef = useRef(performFetch);
  useEffect(() => {
    performFetchRef.current = performFetch;
  }, [performFetch]);

  const debouncedFetch = useMemo(
    () => debounce(() => void performFetchRef.current(), FETCH_DEBOUNCE_MS),
    [],
  );
  useEffect(() => () => debouncedFetch.cancel(), [debouncedFetch]);

  // Skip the very first run: on mount, an edited endpoint's fields are seeded from
  // `initialDraft` along with its already-fetched `fetchedInfo`, so re-fetching would
  // just repeat a request that already has a correct answer. Only field edits made
  // after mount should schedule a re-fetch.
  const skippedInitialFetchTrigger = useRef(false);
  useEffect(() => {
    if (!skippedInitialFetchTrigger.current) {
      skippedInitialFetchTrigger.current = true;
      return;
    }
    if (!trimmedUrl) return;
    debouncedFetch();
  }, [trimmedUrl, authHeader, authValue, authEnabled, debouncedFetch]);

  const handleAdd = useCallback(() => {
    if (!fetchedInfo || selectedEnvIds.length === 0) return;
    onAdd({
      name: name.trim(),
      url: trimmedUrl,
      authHeader: effectiveAuthHeader,
      authValue: resolvedAuthValue,
      environments: selectedEnvIds,
      gatewayByEnv: effectiveGatewayByEnv,
      fetchedInfo,
      serverName:
        getServerInfoValue(fetchedInfo.serverInfo, "name") ??
        initialDraft?.serverName,
      serverVersion:
        getServerInfoValue(fetchedInfo.serverInfo, "version") ??
        initialDraft?.serverVersion,
    });
  }, [
    effectiveAuthHeader,
    resolvedAuthValue,
    effectiveGatewayByEnv,
    fetchedInfo,
    initialDraft,
    name,
    onAdd,
    selectedEnvIds,
    trimmedUrl,
  ]);

  const serverName = fetchedInfo
    ? getServerInfoValue(fetchedInfo.serverInfo, "name")
    : undefined;
  const serverVersion = fetchedInfo
    ? getServerInfoValue(fetchedInfo.serverInfo, "version")
    : undefined;

  return (
    <Form.Stack spacing={2.5}>
      <FormControl fullWidth>
        <FormLabel>Endpoint Name</FormLabel>
        <TextField
          fullWidth
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="Primary"
          helperText="Optional. A handle is derived from the name (or URL) when left blank."
        />
      </FormControl>

      <FormControl fullWidth error={Boolean(urlError)}>
        <FormLabel required>MCP Server Endpoint URL</FormLabel>
        <TextField
          fullWidth
          value={url}
          onChange={(event) => {
            setUrl(event.target.value);
            clearFetched();
            setUrlError(null);
          }}
          placeholder="Enter URL of your MCP Server"
          error={Boolean(urlError)}
          helperText={urlError}
        />
      </FormControl>

      <Accordion
        expanded={advancedOpen}
        onChange={(_, expanded) => setAdvancedOpen(expanded)}
        disableGutters
        variant="outlined"
      >
        <AccordionSummary expandIcon={<ChevronDown size={18} />}>
          <Stack direction="row" alignItems="center" spacing={1}>
            <Typography variant="subtitle2" fontWeight={600}>
              Advanced Configurations
            </Typography>
            <Tooltip title="Configure an optional authentication header sent to the MCP server.">
              <HelpCircle size={16} />
            </Tooltip>
          </Stack>
        </AccordionSummary>
        <AccordionDetails>
          <AuthHeaderRow
            enabled={authEnabled}
            onEnabledChange={(enabled) => {
              setAuthEnabled(enabled);
              clearFetched();
              setAuthError(null);
            }}
            headerValue={authHeader}
            onHeaderChange={(value) => {
              setAuthHeader(value);
              clearFetched();
              setAuthError(null);
            }}
            valueValue={authValue}
            onValueFocus={() => {
              // Reveal a blank field so the user replaces the hidden
              // stored credential rather than editing the mask.
              if (isCredentialMasked) {
                setAuthValue("");
                setIsCredentialMasked(false);
                clearFetched();
              }
            }}
            onValueChange={(value) => {
              setAuthValue(value);
              setIsCredentialMasked(false);
              clearFetched();
              setAuthError(null);
            }}
            showValue={showAuthValue}
            onToggleShowValue={() => setShowAuthValue((prev) => !prev)}
            error={Boolean(authError)}
            caption={
              authError ??
              (isCredentialMasked
                ? "Leave unchanged to keep the stored value."
                : null)
            }
            captionColor={authError ? "error" : "text.secondary"}
          />
        </AccordionDetails>
      </Accordion>

      {availableEnvironments.length !== 1 && (
        <FormControl fullWidth>
          <FormLabel required={availableEnvironments.length > 1}>
            Environments
          </FormLabel>
          {availableEnvironments.length > 1 ? (
            <>
              <Autocomplete
                multiple
                options={availableEnvironments}
                size="small"
                value={availableEnvironments.filter(
                  (env) => env.id != null && selectedEnvIds.includes(env.id),
                )}
                onChange={(_, selected) =>
                  setSelectedEnvIds(
                    selected
                      .map((env) => env.id)
                      .filter((id): id is string => Boolean(id)),
                  )
                }
                getOptionLabel={(option) => option.displayName || option.name}
                isOptionEqualToValue={(option, value) => option.id === value.id}
                renderInput={(params) => (
                  <TextField {...params} placeholder="Select environment(s)" />
                )}
                sx={{ mt: 0.5 }}
              />
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ mt: 0.5 }}
              >
                An environment can be assigned to only one endpoint.
              </Typography>
            </>
          ) : (
            <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
              All environments are already assigned
            </Typography>
          )}
        </FormControl>
      )}

      {selectedEnvIds.map((envId) => {
        const candidates = egressGatewaysByEnv[envId] ?? [];
        // 0 or 1 candidate needs no picker: the server infers the sole one (or
        // there's nothing to deploy to yet — the ingress-only case).
        if (candidates.length <= 1) return null;
        const deployed = deployedGatewayByEnv[envId];
        return (
          <FormControl fullWidth key={envId}>
            <FormLabel required>
              Egress gateway — {environmentNameById[envId] ?? envId}
            </FormLabel>
            <Select
              size="small"
              value={deployed ?? selectedGatewayByEnv[envId] ?? ""}
              disabled={Boolean(deployed)}
              onChange={(e) =>
                setSelectedGatewayByEnv({
                  ...selectedGatewayByEnv,
                  [envId]: e.target.value as string,
                })
              }
            >
              {candidates.map((gw) => (
                <MenuItem key={gw.uuid} value={gw.uuid}>
                  {gw.displayName || gw.name}
                </MenuItem>
              ))}
            </Select>
            {deployed ? (
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ mt: 0.5 }}
              >
                Placement is fixed once deployed. Delete and recreate the
                binding to change it.
              </Typography>
            ) : (
              !selectedGatewayByEnv[envId] && (
                <Typography variant="caption" color="error" sx={{ mt: 0.5 }}>
                  Select an egress gateway for this environment.
                </Typography>
              )
            )}
          </FormControl>
        );
      })}

      <Collapse in={isFetching || isFetched} timeout="auto" unmountOnExit>
        <Stack spacing={2}>
          <Divider />
          {isFetching ? (
            <Stack direction="row" spacing={1.5} alignItems="center">
              <CircularProgress size={18} />
              <Typography variant="body2" color="text.secondary">
                Fetching server info...
              </Typography>
            </Stack>
          ) : fetchedInfo ? (
            <Stack spacing={2}>
              <Stack direction="row" spacing={1} alignItems="center">
                <Typography variant="h6" fontWeight={600}>
                  {serverName || "MCP Server"}
                </Typography>
                {serverVersion ? (
                  <Chip
                    label={
                      serverVersion.startsWith("v")
                        ? serverVersion
                        : `v${serverVersion}`
                    }
                    size="small"
                    variant="outlined"
                  />
                ) : null}
              </Stack>
              <MCPCapabilitiesView
                tools={fetchedInfo.tools}
                resources={fetchedInfo.resources}
                prompts={fetchedInfo.prompts}
              />
            </Stack>
          ) : null}
        </Stack>
      </Collapse>

      <Box display="flex" justifyContent="flex-end" gap={1}>
        <Button variant="outlined" onClick={onCancel} disabled={isFetching}>
          Cancel
        </Button>
        {/* Add/Update requires a completed fetch (canAdd depends on isFetched), so
            any field change that clears the fetch disables it until the debounced
            re-fetch completes. */}
        <Button
          variant="contained"
          onClick={handleAdd}
          disabled={!canSave || isFetching}
        >
          {isEditing ? "Update Endpoint" : "Add Endpoint"}
        </Button>
      </Box>
    </Form.Stack>
  );
}

function getServerInfoValue(
  serverInfo: Record<string, unknown> | undefined,
  key: string,
): string | undefined {
  const value = serverInfo?.[key];
  return typeof value === "string" ? value : undefined;
}

// Order-insensitive equality for the selected environment IDs.
function sameIdSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const setB = new Set(b);
  return a.every((id) => setB.has(id));
}

// Whether the per-environment gateway selection matches the stored one.
function sameGatewayMap(
  a: Record<string, string>,
  b: Record<string, string>,
): boolean {
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => a[key] === b[key]);
}

// Whether a re-fetch produced capabilities that differ from the stored ones. Only the
// tool/resource/prompt lists are compared, since those are what gets persisted.
function capabilitiesChanged(
  current: MCPServerInfoFetchResponse | null,
  original: MCPServerInfoFetchResponse,
): boolean {
  if (!current) return false;
  return (
    JSON.stringify(current.tools ?? []) !==
      JSON.stringify(original.tools ?? []) ||
    JSON.stringify(current.resources ?? []) !==
      JSON.stringify(original.resources ?? []) ||
    JSON.stringify(current.prompts ?? []) !==
      JSON.stringify(original.prompts ?? [])
  );
}
