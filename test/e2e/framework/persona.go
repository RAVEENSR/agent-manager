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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RolePersona is a disposable OAuth application assigned to a real role in
// Thunder. It lets black-box security tests exercise deployed role-to-scope
// bindings without automating an interactive browser login or retaining a
// permanent test identity.
type RolePersona struct {
	Name            string
	RoleName        string
	RolePermissions []string
	Token           string

	appID        string
	roleID       string
	clientID     string
	clientSecret string
}

// AudienceClient is a disposable, unassigned OAuth application used to test
// audience-based boundaries independently from roles and scopes.
type AudienceClient struct {
	Name  string
	Token string

	appID        string
	clientID     string
	clientSecret string
}

// PersonaProvisioner manages short-lived role personas through Thunder's
// administrative API. Its credentials should be supplied as CI secrets outside
// local quick-start, where the documented bootstrap defaults are sufficient.
type PersonaProvisioner struct {
	cfg         *Config
	httpClient  *http.Client
	systemToken string
}

type thunderRole struct {
	ID          string `json:"id"`
	OuID        string `json:"ouId"`
	Name        string `json:"name"`
	Permissions []struct {
		Permissions []string `json:"permissions"`
	} `json:"permissions"`
}

// NewPersonaProvisioner authenticates the configured Thunder system client.
func NewPersonaProvisioner(ctx context.Context, cfg *Config) (*PersonaProvisioner, error) {
	return newPersonaProvisionerWithHTTPClient(ctx, cfg, &http.Client{Timeout: 30 * time.Second})
}

func newPersonaProvisionerWithHTTPClient(ctx context.Context, cfg *Config, httpClient *http.Client) (*PersonaProvisioner, error) {
	p := &PersonaProvisioner{
		cfg:        cfg,
		httpClient: httpClient,
	}
	token, err := p.fetchToken(ctx, cfg.ThunderSystemClientID, cfg.ThunderSystemClientSecret,
		[]string{"system"}, cfg.ThunderSystemResource)
	if err != nil {
		return nil, fmt.Errorf("authenticate Thunder system client: %w", err)
	}
	p.systemToken = token
	return p, nil
}

// CreateRolePersona creates an OAuth application, assigns the named deployed
// role, and obtains a token requesting the entire AMP scope catalog. Thunder
// returns requested ∩ role permissions, which the caller must verify.
func (p *PersonaProvisioner) CreateRolePersona(ctx context.Context, roleName string) (*RolePersona, error) {
	ouID, err := p.defaultOUID(ctx)
	if err != nil {
		return nil, err
	}
	role, err := p.findRole(ctx, ouID, roleName)
	if err != nil {
		return nil, err
	}

	name := "e2e-test-sec-persona-" + strings.ReplaceAll(roleName, "-", "") + "-" + uuid.NewString()[:8]
	appID, clientID, clientSecret, err := p.createApplication(ctx, ouID, name)
	if err != nil {
		return nil, err
	}
	persona := &RolePersona{
		Name:         name,
		RoleName:     roleName,
		appID:        appID,
		roleID:       role.ID,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
	for _, group := range role.Permissions {
		persona.RolePermissions = append(persona.RolePermissions, group.Permissions...)
	}

	cleanupOnError := func(cause error) (*RolePersona, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if cleanupErr := p.DeleteRolePersona(cleanupCtx, persona); cleanupErr != nil {
			return nil, fmt.Errorf("%w (additionally failed to clean up %s: %v)", cause, name, cleanupErr)
		}
		return nil, cause
	}

	assignment := map[string]any{"assignments": []map[string]string{{"id": appID, "type": "app"}}}
	if err := p.doJSON(ctx, http.MethodPost, "/roles/"+role.ID+"/assignments/add", assignment, nil); err != nil {
		return cleanupOnError(fmt.Errorf("assign application %s to role %s: %w", name, roleName, err))
	}

	// Role assignment can be eventually visible to token issuance. Retry for a
	// short bounded period and require the resulting token to contain at least
	// one role permission before returning it to the tests.
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		persona.Token, lastErr = p.fetchToken(ctx, clientID, clientSecret, AllScopes(), "")
		if lastErr == nil {
			scopes, decodeErr := TokenScopes(persona.Token)
			if decodeErr == nil && (len(persona.RolePermissions) == 0 || hasAnyScope(scopes, persona.RolePermissions)) {
				return persona, nil
			}
			lastErr = fmt.Errorf("token contains none of role %s's permissions", roleName)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return cleanupOnError(fmt.Errorf("mint token for role persona %s: %w", roleName, lastErr))
}

// RefreshRolePersonaToken mints a new token for an existing disposable
// persona. It is intentionally separate from CreateRolePersona so revocation
// tests can distinguish an already-issued JWT from policy applied to a newly
// issued token.
func (p *PersonaProvisioner) RefreshRolePersonaToken(ctx context.Context, persona *RolePersona) (string, error) {
	if persona == nil || persona.clientID == "" || persona.clientSecret == "" {
		return "", fmt.Errorf("role persona has no active OAuth credentials")
	}
	token, err := p.fetchToken(ctx, persona.clientID, persona.clientSecret, AllScopes(), "")
	if err != nil {
		return "", fmt.Errorf("refresh token for role persona %s: %w", persona.RoleName, err)
	}
	persona.Token = token
	return token, nil
}

// CreateAudienceClient creates an unassigned OAuth client whose client ID (and
// therefore default client-credentials audience) starts with namePrefix. No AMP
// scopes are requested, keeping audience tests independent from role policy.
func (p *PersonaProvisioner) CreateAudienceClient(ctx context.Context, namePrefix string) (*AudienceClient, error) {
	ouID, err := p.defaultOUID(ctx)
	if err != nil {
		return nil, err
	}
	name := namePrefix + uuid.NewString()[:8]
	appID, clientID, clientSecret, err := p.createApplication(ctx, ouID, name)
	if err != nil {
		return nil, err
	}
	client := &AudienceClient{
		Name:         name,
		appID:        appID,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
	cleanupOnError := func(cause error) (*AudienceClient, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if cleanupErr := p.DeleteAudienceClient(cleanupCtx, client); cleanupErr != nil {
			return nil, fmt.Errorf("%w (additionally failed to clean up %s: %v)", cause, name, cleanupErr)
		}
		return nil, cause
	}

	client.Token, err = p.fetchToken(ctx, clientID, clientSecret, nil, "")
	if err != nil {
		return cleanupOnError(fmt.Errorf("mint token for audience client %s: %w", name, err))
	}
	audiences, err := TokenAudiences(client.Token)
	if err != nil {
		return cleanupOnError(fmt.Errorf("decode audience client token: %w", err))
	}
	if !slices.Contains(audiences, clientID) {
		return cleanupOnError(fmt.Errorf("Thunder token audience %v does not contain client ID %q", audiences, clientID))
	}
	return client, nil
}

// DeleteAudienceClient removes a disposable unassigned OAuth application.
func (p *PersonaProvisioner) DeleteAudienceClient(ctx context.Context, client *AudienceClient) error {
	if client == nil || client.appID == "" {
		return nil
	}
	if err := p.doJSON(ctx, http.MethodDelete, "/applications/"+client.appID, nil, nil); err != nil {
		return fmt.Errorf("delete audience client %s: %w", client.Name, err)
	}
	client.appID = ""
	client.clientID = ""
	client.clientSecret = ""
	return nil
}

// DeleteRolePersona removes the role assignment and application. Application
// deletion is attempted even if assignment removal fails, so failed test runs
// do not retain credentials.
func (p *PersonaProvisioner) DeleteRolePersona(ctx context.Context, persona *RolePersona) error {
	if persona == nil || persona.appID == "" {
		return nil
	}
	assignment := map[string]any{"assignments": []map[string]string{{"id": persona.appID, "type": "app"}}}
	removeErr := p.doJSON(ctx, http.MethodPost, "/roles/"+persona.roleID+"/assignments/remove", assignment, nil)
	deleteErr := p.doJSON(ctx, http.MethodDelete, "/applications/"+persona.appID, nil, nil)
	if removeErr != nil && deleteErr != nil {
		return fmt.Errorf("remove assignment: %v; delete application: %w", removeErr, deleteErr)
	}
	if deleteErr != nil {
		return fmt.Errorf("delete application: %w", deleteErr)
	}
	persona.clientID = ""
	persona.clientSecret = ""
	// A deleted application cannot use its assignment, so a removal failure is
	// not a leaked credential. Thunder also removes dangling app assignments.
	return nil
}

func (p *PersonaProvisioner) defaultOUID(ctx context.Context) (string, error) {
	var result struct {
		ID string `json:"id"`
	}
	if err := p.doJSON(ctx, http.MethodGet, "/organization-units/tree/default", nil, &result); err != nil {
		return "", fmt.Errorf("get Thunder default organization unit: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("Thunder default organization unit response has no id")
	}
	return result.ID, nil
}

func (p *PersonaProvisioner) findRole(ctx context.Context, ouID, name string) (*thunderRole, error) {
	for offset := 0; offset < 10000; offset += 100 {
		var result struct {
			Roles        []thunderRole `json:"roles"`
			TotalResults int           `json:"totalResults"`
		}
		path := fmt.Sprintf("/roles?offset=%d&limit=100", offset)
		if err := p.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
			return nil, fmt.Errorf("list Thunder roles: %w", err)
		}
		for i := range result.Roles {
			if result.Roles[i].OuID == ouID && result.Roles[i].Name == name {
				var complete thunderRole
				if err := p.doJSON(ctx, http.MethodGet, "/roles/"+result.Roles[i].ID, nil, &complete); err != nil {
					return nil, fmt.Errorf("get Thunder role %s: %w", name, err)
				}
				return &complete, nil
			}
		}
		if offset+len(result.Roles) >= result.TotalResults || len(result.Roles) == 0 {
			break
		}
	}
	return nil, fmt.Errorf("deployed Thunder role %q not found in the default organization unit", name)
}

func (p *PersonaProvisioner) createApplication(ctx context.Context, ouID, name string) (string, string, string, error) {
	payload := map[string]any{
		"name": name,
		"type": "m2m",
		"ouId": ouID,
		"inboundAuthConfig": []map[string]any{{
			"type": "oauth2",
			"config": map[string]any{
				"clientId":                name,
				"grantTypes":              []string{"client_credentials"},
				"tokenEndpointAuthMethod": "client_secret_basic",
				"scopes":                  AllScopes(),
				"token": map[string]any{
					"accessToken": map[string]any{
						"clientConfig": map[string]any{
							"attributes": []string{"ouId", "ouHandle"},
						},
					},
				},
			},
		}},
	}
	var result struct {
		ID           string `json:"id"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		InboundAuth  []struct {
			Config struct {
				ClientID     string `json:"clientId"`
				ClientSecret string `json:"clientSecret"`
			} `json:"config"`
		} `json:"inboundAuthConfig"`
	}
	if err := p.doJSON(ctx, http.MethodPost, "/applications", payload, &result); err != nil {
		return "", "", "", fmt.Errorf("create Thunder persona application: %w", err)
	}
	if len(result.InboundAuth) > 0 {
		if result.ClientID == "" {
			result.ClientID = result.InboundAuth[0].Config.ClientID
		}
		if result.ClientSecret == "" {
			result.ClientSecret = result.InboundAuth[0].Config.ClientSecret
		}
	}
	if result.ID == "" || result.ClientID == "" || result.ClientSecret == "" {
		return "", "", "", fmt.Errorf("Thunder application response omitted id or OAuth credentials")
	}
	return result.ID, result.ClientID, result.ClientSecret, nil
}

func (p *PersonaProvisioner) fetchToken(ctx context.Context, clientID, clientSecret string, scopes []string, resource string) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.ThunderAdminURL, "/")+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned an empty access token")
	}
	return result.AccessToken, nil
}

func (p *PersonaProvisioner) doJSON(ctx context.Context, method, path string, payload, result any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.cfg.ThunderAdminURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.systemToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Thunder %s %s returned %d: %s", method, path, resp.StatusCode, string(responseBody))
	}
	if result != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, result); err != nil {
			return fmt.Errorf("decode Thunder %s %s response: %w", method, path, err)
		}
	}
	return nil
}

func hasAnyScope(actual map[string]struct{}, expected []string) bool {
	for _, scope := range expected {
		if _, ok := actual[scope]; ok {
			return true
		}
	}
	return false
}
