// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientCredentialsTokenResult captures both successful and rejected OAuth2
// client_credentials responses without logging the supplied client secret.
type ClientCredentialsTokenResult struct {
	StatusCode       int
	AccessToken      string
	TokenType        string
	OAuthError       string
	ErrorDescription string
}

// RequestClientCredentialsToken makes one OAuth2 client_credentials request.
// Security lifecycle tests deliberately need both the success and invalid_client
// responses, so a non-200 HTTP status is returned as data rather than as an error.
func RequestClientCredentialsToken(ctx context.Context, tokenURL, clientID, clientSecret string) (ClientCredentialsTokenResult, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ClientCredentialsTokenResult{}, fmt.Errorf("create client-credentials request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	if parsed, parseErr := url.Parse(tokenURL); parseErr == nil && parsed.Hostname() != "" {
		req.Host = parsed.Host
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return ClientCredentialsTokenResult{}, fmt.Errorf("request client-credentials token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ClientCredentialsTokenResult{}, fmt.Errorf("read client-credentials response: %w", err)
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		OAuthError       string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ClientCredentialsTokenResult{}, fmt.Errorf("decode client-credentials response (status %d): %w", resp.StatusCode, err)
	}

	return ClientCredentialsTokenResult{
		StatusCode:       resp.StatusCode,
		AccessToken:      payload.AccessToken,
		TokenType:        payload.TokenType,
		OAuthError:       payload.OAuthError,
		ErrorDescription: payload.ErrorDescription,
	}, nil
}
