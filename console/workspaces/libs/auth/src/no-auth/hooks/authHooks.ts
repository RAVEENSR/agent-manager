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


import type { UserInfo } from '../../types';

const demoUserInfo : UserInfo = {
  username: 'john.doe',
  displayName: 'John Doe',
  orgHandle: 'default',
  orgId: 'default',
  orgName: 'Default',
  sessionState: '',
  sub: '8f307351-25c5-4fc6-85e0-f51c2d458f06',
  allowedScopes: "openid email profile amp:*",
};

export const useAuthHooks = () => {
  return {
    isAuthenticated: true,
    userInfo: demoUserInfo,
    isLoadingUserInfo: false,
    isLoadingIsAuthenticated: false,
    // Nothing is decoded on this path: the demo user's scopes are a constant.
    isLoadingAccessToken: false,
    login: () => Promise.resolve(),
    logout: () => Promise.resolve(),
    trySignInSilently: () => Promise.resolve(),
    getToken: () => Promise.resolve('eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJBZ2VudCBNYW5hZ2VtZW50IFBsYXRmb3JtIExvY2FsIiwiaWF0IjoxNzYxNzI3NDY5LCJleHAiOjE3OTMyNjM0NjksImF1ZCI6ImxvY2FsaG9zdCIsInN1YiI6IjhmMzA3MzUxLTI1YzUtNGZjNi04NWUwLWY1MWMyZDQ1OGYwNiIsIm91SWQiOiJmYWExODNmMS0zOTgzLTRmNjMtYmIzNS04NmZhNzIzZmQ1ZWYiLCJzY29wZSI6Im9wZW5pZCBwcm9maWxlIGVtYWlsIGxsbS1wcm94eTpkZXBsb3kgbWNwLXNlcnZlcjpjcmVhdGUgb3JnOmludml0ZS1tZW1iZXIgcm9sZTpkZWxldGUgbW9uaXRvcjpyZWFkIGFnZW50OmRlcGxveTpwcm9kdWN0aW9uIGdpdC1zZWNyZXQ6ZGVsZXRlIGxsbS1wcm92aWRlcjpkZWxldGUgbWNwLXNlcnZlcjpjb25maWd1cmUtZ3VhcmRyYWlsIGFnZW50OnByb21vdGUgZGVwbG95bWVudC1waXBlbGluZTpyZWFkIGdhdGV3YXk6ZGVsZXRlIGdyb3VwOmRlbGV0ZSBsbG0tcHJveHk6dXBkYXRlIG1vbml0b3I6c2NvcmU6cmVhZCBsbG0tcHJvdmlkZXItdGVtcGxhdGU6dXBkYXRlIGFnZW50OmJ1aWxkIGFnZW50OnJvbGxiYWNrIGFnZW50OnRva2VuOm1hbmFnZSBldmFsdWF0b3I6cmVhZCBsbG0tcHJvdmlkZXItdGVtcGxhdGU6cmVhZCBtb25pdG9yOmNyZWF0ZSBhZ2VudDpzdXNwZW5kIGdhdGV3YXk6dG9rZW46bWFuYWdlIGdpdC1zZWNyZXQ6cmVhZCBtY3Atc2VydmVyOmNvbm5lY3QgbWNwLXNlcnZlcjpyZWFkIG9ic2VydmFiaWxpdHk6cHJvamVjdC1kYXNoYm9hcmQgZ3JvdXA6Y3JlYXRlIG9ic2VydmFiaWxpdHk6b3JnLWRhc2hib2FyZCBvcmc6cmVtb3ZlLW1lbWJlciBhZ2VudDpkZXBsb3k6bm9uLXByb2R1Y3Rpb24gZ3JvdXA6cmVhZCBsbG0tcHJvdmlkZXItdGVtcGxhdGU6Y3JlYXRlIHByb2plY3Q6ZGVsZXRlIGFnZW50OmNyZWF0ZSBldmFsdWF0b3I6Y3JlYXRlIGxsbS1wcm92aWRlcjpyZWFkIG9yZzptb2RpZnktc2V0dGluZ3MgY2F0YWxvZzpyZWFkIGVudmlyb25tZW50OnVwZGF0ZSBncm91cDp1cGRhdGUgbGxtLXByb3h5OmRlbGV0ZSBwcm9qZWN0OnJlYWQgZGF0YS1wbGFuZTpyZWFkIGFnZW50OnJlYWQgZW52aXJvbm1lbnQ6cmVhZCBsbG0tcHJvdmlkZXI6Y29uZmlndXJlLWd1YXJkcmFpbCBvYnNlcnZhYmlsaXR5Omd1YXJkcmFpbC1tZXRyaWMgb3JnOmFzc2lnbi1yb2xlIHByb2plY3Q6dXBkYXRlIG1jcC1zZXJ2ZXI6ZGVsZXRlIGVudmlyb25tZW50OmRlbGV0ZSBldmFsdWF0b3I6dXBkYXRlIGdpdC1zZWNyZXQ6Y3JlYXRlIG1vbml0b3I6ZXhlY3V0ZSBsbG0tcHJvdmlkZXI6YXBpLWtleTptYW5hZ2UgZ2F0ZXdheTpyZWFkIGFnZW50OnVwZGF0ZSBsbG0tcHJvdmlkZXItdGVtcGxhdGU6ZGVsZXRlIG1vbml0b3I6ZGVsZXRlIG9yZzp2aWV3IGxsbS1wcm92aWRlcjpjcmVhdGUgbWNwLXNlcnZlcjp1cGRhdGUgb3JnOm1hbmFnZS1pZHAgcm9sZTpyZWFkIHJvbGU6dXBkYXRlIGFnZW50OmRlbGV0ZSBnYXRld2F5OmNyZWF0ZSBsbG0tcHJvdmlkZXI6ZGVwbG95IGxsbS1wcm94eTphcGkta2V5Om1hbmFnZSBtb25pdG9yOnVwZGF0ZSBvcmc6bWFuYWdlLXNlcnZpY2UtYWNjb3VudCByb2xlOmNyZWF0ZSBldmFsdWF0b3I6ZGVsZXRlIGxsbS1wcm92aWRlcjpjb25uZWN0IGxsbS1wcm92aWRlcjp1cGRhdGUgbGxtLXByb3h5OnJlYWQgbW9uaXRvcjpzY29yZTpwdWJsaXNoIG9ic2VydmFiaWxpdHk6aW5mcmEtbWV0cmljIHByb2plY3Q6Y3JlYXRlIHJlcG9zaXRvcnk6cmVhZCBnYXRld2F5OnVwZGF0ZSBlbnZpcm9ubWVudDpjcmVhdGUgbGxtLXByb3h5OmNyZWF0ZSJ9._sTlFbVnzond_MQkXQYTPLqK35i2kFelz6_EjRdsPTA'),
  };
};
