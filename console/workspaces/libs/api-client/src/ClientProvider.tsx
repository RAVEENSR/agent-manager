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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const STALE_TIME = 8000;
const MAX_QUERY_RETRIES = 3;

// Most 4xx responses (404, 401, 403, 409, ...) mean the request itself won't
// succeed no matter how many times it's repeated, so retrying just delays the
// error reaching the UI. 408 (Request Timeout) is the one exception — it's the
// server giving up on that particular attempt, not rejecting the request
// itself, so it's worth retrying like a network failure / 5xx would be.
function shouldRetryQuery(failureCount: number, error: unknown): boolean {
    const status = (error as { status?: number })?.status;
    const isTerminalClientError =
        status !== undefined && status >= 400 && status < 500 && status !== 408;
    if (isTerminalClientError) {
        return false;
    }
    return failureCount < MAX_QUERY_RETRIES;
}

const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            refetchOnWindowFocus: true,
            staleTime: STALE_TIME,
            retry: shouldRetryQuery,
        },
    },
});

export function ClientProvider({ children }: { children: React.ReactNode }) {

    return (
        <QueryClientProvider client={queryClient}>
            {children}
        </QueryClientProvider>
    );
}

export default ClientProvider;
