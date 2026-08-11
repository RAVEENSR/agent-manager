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

import {
  useGetAgentConfigurations,
  useGetAgentEndpoints,
  useTestAgentAPIKey,
} from "@agent-management-platform/api-client";
import { getErrorMessage } from "@agent-management-platform/shared-component";
import { Alert, Box, Button, Skeleton, Typography } from "@wso2/oxygen-ui";
import { useParams } from "react-router-dom";
import { useMemo, useState, lazy, Suspense } from "react";

const SwaggerUI = lazy(() => import("swagger-ui-react"));

// A freshly issued test key takes a couple of seconds to reach the gateway's
// policy engine, so one delayed retry with the same key rides out that window.
const TEST_KEY_PROPAGATION_RETRY_DELAY_MS = 2000;

const disableAuthorizeAndInfoPluginCustomSecuritySchema = {
  statePlugins: {
    spec: {
      wrapSelectors: {
        servers: () => (): any[] => [],
        schemes: () => (): any[] => [],
      },
    },
  },
  wrapComponents: {
    info: () => (): any => null,
  },
};

export function Swagger() {
  const { orgId, projectId, agentId, envId } = useParams();
  const { data, isLoading, error } = useGetAgentEndpoints(
    {
      agentName: agentId,
      orgName: orgId,
      projName: projectId,
    },
    {
      environment: envId ?? "",
    }
  );

  // GetAgent returns only the lowest environment's config, so read per-env here.
  const { data: envConfig } = useGetAgentConfigurations(
    {
      orgName: orgId,
      projName: projectId,
      agentName: agentId,
    },
    {
      environment: envId ?? "",
    }
  );
  // Absent means no per-env config row yet, so assume a key is required.
  const securityEnabled = envConfig?.enableApiKeySecurity ?? true;
  const oauthOnly = !!(
    envConfig?.enableOAuthSecurity && !envConfig?.enableApiKeySecurity
  );
  const {
    data: testKey,
    isLoading: isLoadingTestKey,
    isError: isTestKeyError,
    error: testKeyError,
    refetch: refetchTestKey,
  } = useTestAgentAPIKey(
    { orgName: orgId, projName: projectId, agentName: agentId, envId },
    { enabled: securityEnabled && !oauthOnly },
  );
  const testApiKey = testKey?.apiKey;
  const [keyAlert, setKeyAlert] = useState<"unauthorized" | "refreshed" | null>(
    null,
  );
  const [isRefreshingKey, setIsRefreshingKey] = useState(false);

  const gatewayOffline = testKey?.gatewayConnected === false;

  const endpoint = useMemo(() => Object.keys(data ?? {})?.[0] ?? "", [data]);

  // The gateway drops CORS headers on 401s, so a cross-origin 401 rejects
  // fetch with a TypeError before swagger-ui's responseInterceptor runs.
  // Inject a custom fetch (req.userFetch) instead: retry once to ride out key
  // propagation, then surface the refresh alert on a persistent 401/TypeError.
  const requestInterceptor = useMemo(
    () => (req: any) => {
      const targetUrl = data?.[endpoint]?.url;
      if (!targetUrl) {
        return req;
      }
      const incoming = new URL(req.url, window.location.origin);
      const target = new URL(targetUrl);

      const targetPath = target.pathname.replace(/\/+$/, "");
      const incomingPath = incoming.pathname.replace(/^\/+/, "");
      const mergedPath = [targetPath, incomingPath].filter(Boolean).join("/");

      target.pathname = mergedPath.startsWith("/")
        ? mergedPath
        : `/${mergedPath}`;
      target.search = incoming.search;
      target.hash = incoming.hash;
      req.url = target.toString();
      if (securityEnabled && testApiKey) {
        req.headers = req.headers ?? {};
        req.headers["X-API-Key"] = testApiKey;
      }

      if (securityEnabled && !gatewayOffline) {
        req.userFetch = async (url: string, options: RequestInit) => {
          const sleep = () =>
            new Promise((resolve) =>
              setTimeout(resolve, TEST_KEY_PROPAGATION_RETRY_DELAY_MS),
            );
          try {
            const res = await fetch(url, options);
            if (res.status !== 401) {
              return res;
            }
            await sleep();
            const retry = await fetch(url, options);
            if (retry.status === 401) {
              setKeyAlert("unauthorized");
            }
            return retry;
          } catch (err) {
            if (!(err instanceof TypeError)) {
              throw err;
            }
            await sleep();
            try {
              return await fetch(url, options);
            } catch (retryErr) {
              setKeyAlert("unauthorized");
              throw retryErr;
            }
          }
        };
      }
      return req;
    },
    [data, endpoint, securityEnabled, testApiKey, gatewayOffline]
  );

  const handleRefreshTestKey = async () => {
    setIsRefreshingKey(true);
    try {
      await refetchTestKey();
      setKeyAlert("refreshed");
    } finally {
      setIsRefreshingKey(false);
    }
  };

  if (isLoading || (securityEnabled && isLoadingTestKey)) {
    return <Skeleton variant="rounded" height={500} />;
  }

  if (error) {
    return <Alert severity="error">{getErrorMessage(error)}</Alert>;
  }

  if (securityEnabled && isTestKeyError) {
    return (
      <Alert severity="error">
        Failed to fetch test API key{testKeyError instanceof Error ? `: ${testKeyError.message}` : ""}.
      </Alert>
    );
  }

  if (!data?.[endpoint]?.schema?.content) {
    return (
      <Alert severity="warning">
        No API schema available for this endpoint.
      </Alert>
    );
  }

  return (
    <Suspense fallback={<Skeleton variant="rounded" height={500} />}>
      {oauthOnly && (
        <Alert severity="info" sx={{ mb: 2 }}>
          <Typography variant="caption">
            OAuth is enabled — test this endpoint out-of-band with an{" "}
            <code>Authorization: Bearer &lt;token&gt;</code> header.
          </Typography>
        </Alert>
      )}
      {securityEnabled && gatewayOffline && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          The gateway is not connected to the control plane right now. The
          test API key has been stored but will only work once the gateway
          reconnects.
        </Alert>
      )}
      {keyAlert === "unauthorized" && (
        <Alert
          severity="error"
          action={
            <Button
              color="inherit"
              size="small"
              onClick={handleRefreshTestKey}
              disabled={isRefreshingKey}
            >
              {isRefreshingKey ? "Refreshing..." : "Refresh test key"}
            </Button>
          }
          sx={{ mb: 2 }}
        >
          The test API key is not authorized on the gateway yet. Execute the
          request again, or refresh the test key.
        </Alert>
      )}
      {keyAlert === "refreshed" && (
        <Alert
          severity="info"
          onClose={() => setKeyAlert(null)}
          sx={{ mb: 2 }}
        >
          Test key refreshed. Execute the request again.
        </Alert>
      )}
      <Box sx={{ "& .swagger-ui .wrapper": { padding: 0 } }}>
        <SwaggerUI
          spec={data?.[endpoint].schema.content}
          layout="BaseLayout"
          plugins={[disableAuthorizeAndInfoPluginCustomSecuritySchema]}
          docExpansion="list"
          requestInterceptor={requestInterceptor}
          supportedSubmitMethods={oauthOnly ? [] : undefined}
        />
      </Box>
    </Suspense>
  );
}
