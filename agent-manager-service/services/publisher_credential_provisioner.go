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

package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	ocauth "github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/auth"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/clients/secretmanagersvc"
	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const schedulerRoleName = "amp-monitor-scheduler"

// ErrNotThunderMode is returned by GetOCClientForOrg when the provisioner is not in Thunder mode.
var ErrNotThunderMode = errors.New("not in Thunder mode")

// ErrPublisherCredentialNotFound indicates EnsureCredentials has not yet been called for the org.
// Distinct from real DB errors so callers can decide whether to provision-on-demand vs retry.
var ErrPublisherCredentialNotFound = errors.New("publisher credentials not found")

// ErrSchedulerCredentialNotFound means the org has no scheduler credential the scheduler can
// use — either no row at all, or one carrying no usable secret. Both are repaired only on the
// request path, so the scheduler reports this and moves on rather than trying to fix it.
var ErrSchedulerCredentialNotFound = errors.New("scheduler credentials not found")

// PublisherCredentials holds the provisioned OAuth2 credentials for publishing scores.
type PublisherCredentials struct {
	ClientID     string // OAuth2 client ID (becomes JWT subject)
	SecretKVPath string // KV path in the secret store (remoteRef.key for ExternalSecret)
	SecretKey    string // Key within the KV secret (remoteRef.property for ExternalSecret)
}

// PublisherCredentialProvisioner provisions per-org publisher credentials.
type PublisherCredentialProvisioner interface {
	// EnsureCredentials provisions per-org publisher credentials.
	// orgUUID is the Thunder organization unit UUID (from JWT ouId claim).
	EnsureCredentials(ctx context.Context, ouID, orgUUID string) (*PublisherCredentials, error)

	// IsThunderMode returns true when Thunder is configured for multi-tenant
	// credential provisioning, false for static single-tenant mode.
	IsThunderMode() bool

	// GetOCClientForOrg returns an OC client authenticated with the org's publisher app token.
	// Used by the scheduler which runs without a user request context and therefore has no
	// user JWT in ctx. Decrypts the stored client secret and exchanges it for an access token
	// via the IDP token endpoint.
	// Strictly read-only with respect to credentials: it never provisions, because doing so
	// needs the very JWT the scheduler lacks. Returns ErrSchedulerCredentialNotFound when the
	// org has no usable credential yet; EnsureCredentials on the request path supplies one.
	// In non-Thunder mode returns nil, ErrNotThunderMode — callers must fall back to the system OC client.
	GetOCClientForOrg(ctx context.Context, ouID string) (client.OpenChoreoClient, error)
}

// staticPublisherCredentialProvisioner returns hardcoded static credentials
// when Thunder is not configured (on-prem single-tenant mode).
type staticPublisherCredentialProvisioner struct {
	creds *PublisherCredentials
}

func (s *staticPublisherCredentialProvisioner) EnsureCredentials(_ context.Context, _, _ string) (*PublisherCredentials, error) {
	return s.creds, nil
}

func (s *staticPublisherCredentialProvisioner) IsThunderMode() bool { return false }

func (s *staticPublisherCredentialProvisioner) GetOCClientForOrg(_ context.Context, _ string) (client.OpenChoreoClient, error) {
	return nil, ErrNotThunderMode
}

// NewStaticPublisherCredentialProvisioner creates a static provisioner for use in tests.
func NewStaticPublisherCredentialProvisioner() PublisherCredentialProvisioner {
	return &staticPublisherCredentialProvisioner{
		creds: &PublisherCredentials{
			ClientID:     "amp-publisher-client",
			SecretKVPath: "amp-publisher-client-secret",
			SecretKey:    "value",
		},
	}
}

// publisherCredentialProvisioner provisions per-org credentials via Thunder + SecretManagementClient.
type publisherCredentialProvisioner struct {
	thunderClient     thundersvc.ThunderClient
	secretClient      secretmanagersvc.SecretManagementClient
	ocClient          client.OpenChoreoClient
	credRepo          repositories.OrgPublisherCredentialRepository
	schedulerCredRepo repositories.OrgSchedulerCredentialRepository
	logger            *slog.Logger
	encryptionKey     []byte
	idpTokenURL       string
	ocBaseURL         string

	sfg singleflight.Group // serializes provisioning and per-org client construction

	// orgOCClients caches per-org OpenChoreoClients so that the underlying http.Client
	// connection pool and the wrapped AuthProvider's token cache are reused across
	// scheduler cycles. Singleflight serializes builders; the lock guards map access only.
	orgOCMu      sync.RWMutex
	orgOCClients map[string]client.OpenChoreoClient
}

// NewPublisherCredentialProvisioner creates a provisioner.
// If Thunder is not configured (BaseURL empty), returns a static provisioner
// that always returns the default amp-publisher-client credentials.
func NewPublisherCredentialProvisioner(
	cfg config.Config,
	encryptionKey []byte,
	logger *slog.Logger,
	secretClient secretmanagersvc.SecretManagementClient,
	ocClient client.OpenChoreoClient,
	credRepo repositories.OrgPublisherCredentialRepository,
	schedulerCredRepo repositories.OrgSchedulerCredentialRepository,
) (PublisherCredentialProvisioner, error) {
	if cfg.Thunder.BaseURL == "" {
		logger.Info("Thunder not configured, using static publisher credentials")
		return &staticPublisherCredentialProvisioner{
			creds: &PublisherCredentials{
				ClientID:     "amp-publisher-client",
				SecretKVPath: "amp-publisher-client-secret",
				SecretKey:    "value",
			},
		}, nil
	}

	thunderCl := thundersvc.NewThunderClient(
		cfg.Thunder.BaseURL,
		cfg.Thunder.ClientID,
		cfg.Thunder.ClientSecret,
	)

	logger.Info(
		"Publisher credential provisioner initialized with Thunder",
		"thunderBaseURL", cfg.Thunder.BaseURL,
	)

	return &publisherCredentialProvisioner{
		thunderClient:     thunderCl,
		secretClient:      secretClient,
		ocClient:          ocClient,
		credRepo:          credRepo,
		schedulerCredRepo: schedulerCredRepo,
		logger:            logger,
		encryptionKey:     encryptionKey,
		idpTokenURL:       cfg.IDP.TokenURL,
		ocBaseURL:         cfg.OpenChoreo.BaseURL,
		orgOCClients:      make(map[string]client.OpenChoreoClient),
	}, nil
}

func (p *publisherCredentialProvisioner) IsThunderMode() bool { return true }

// publisherSecretLocation builds the SecretLocation for publisher credentials.
func publisherSecretLocation(ouID string) secretmanagersvc.SecretLocation {
	return secretmanagersvc.SecretLocation{
		OrgName:    ouID,
		EntityName: "amp-publisher-" + ouID,
	}
}

// schedulerSecretLocation builds the SecretLocation for scheduler-only credentials.
func schedulerSecretLocation(ouID string) secretmanagersvc.SecretLocation {
	return secretmanagersvc.SecretLocation{
		OrgName:    ouID,
		EntityName: "amp-scheduler-" + ouID,
	}
}

// resolveSecretRef fetches the SecretReference via OpenChoreo and extracts
// the remoteRef key and property for the "client-secret" data source.
func (p *publisherCredentialProvisioner) resolveSecretRef(ctx context.Context, ouID, secretRefName string) (kvPath, secretKey string, err error) {
	p.logger.Info("Resolving SecretReference from OpenChoreo",
		"ouID", ouID, "secretRefName", secretRefName)

	ref, err := p.ocClient.GetSecretReference(ctx, ouID, secretRefName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get SecretReference %s: %w", secretRefName, err)
	}

	p.logger.Info("SecretReference fetched",
		"ouID", ouID, "secretRefName", secretRefName, "dataSources", len(ref.Data))

	for _, ds := range ref.Data {
		if ds.SecretKey == "client-secret" {
			return ds.RemoteRef.Key, ds.RemoteRef.Property, nil
		}
	}

	return "", "", fmt.Errorf("SecretReference %s has no \"client-secret\" data source (found %d other sources)",
		secretRefName, len(ref.Data))
}

// EnsureCredentials provisions per-org publisher and scheduler credentials.
// Uses singleflight to deduplicate concurrent provisioning calls for the same org.
func (p *publisherCredentialProvisioner) EnsureCredentials(ctx context.Context, ouID, orgUUID string) (*PublisherCredentials, error) {
	p.logger.Debug("EnsureCredentials called", "ouID", ouID, "orgUUID", orgUUID)

	result, err, _ := p.sfg.Do("provision:"+ouID, func() (any, error) {
		pubCreds, err := p.provisionPublisherCredentials(ctx, ouID, orgUUID)
		if err != nil {
			return nil, err
		}
		if err := p.provisionSchedulerCredentials(ctx, ouID, orgUUID); err != nil {
			return nil, fmt.Errorf("failed to provision scheduler credentials for org %s: %w", ouID, err)
		}
		return pubCreds, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*PublisherCredentials), nil
}

// provisionPublisherCredentials provisions the eval-job pod's credential — bound to no OpenChoreo role.
func (p *publisherCredentialProvisioner) provisionPublisherCredentials(ctx context.Context, ouID, orgUUID string) (*PublisherCredentials, error) {
	// Check DB for existing credentials
	existing, err := p.credRepo.GetByOrgName(ouID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to look up publisher credentials for org %s: %w", ouID, err)
		}
		// ErrRecordNotFound: no credentials yet, fall through to provision.
	} else {
		p.logger.Debug("Found existing publisher credentials in DB",
			"ouID", ouID, "clientID", existing.ClientID)

		return &PublisherCredentials{
			ClientID:     existing.ClientID,
			SecretKVPath: existing.SecretKVPath,
			SecretKey:    existing.SecretKey,
		}, nil
	}

	p.logger.Info("No existing credentials, provisioning via Thunder", "ouID", ouID)

	// Not found — create Thunder OAuth app
	clientID, clientSecret, created, err := p.thunderClient.EnsurePublisherApp(ctx, ouID, orgUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to provision Thunder app for org %s: %w", ouID, err)
	}
	p.logger.Info("Thunder EnsurePublisherApp result",
		"ouID", ouID, "clientID", clientID, "created", created, "hasSecret", clientSecret != "")

	// If app already existed in Thunder but not in DB, clientSecret is empty.
	// Regenerate rather than deleting the whole app.
	if !created && clientSecret == "" {
		p.logger.Warn("Thunder app exists but secret not available — regenerating client secret",
			"ouID", ouID, "clientID", clientID)

		clientSecret, err = p.thunderClient.RegenerateClientSecret(ctx, ouID)
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate client secret for org %s: %w", ouID, err)
		}
		p.logger.Info("Regenerated Thunder client secret",
			"ouID", ouID, "clientID", clientID)
	}

	if clientSecret == "" {
		return nil, fmt.Errorf("failed to provision publisher credentials for org %s: no client secret available", ouID)
	}

	// Store secret via SecretManagementClient (creates KV entry + SecretReference CR)
	location := publisherSecretLocation(ouID)
	secretData := map[string]string{
		"client-id":     clientID,
		"client-secret": clientSecret,
	}

	secretRefName, createErr := p.secretClient.CreateSecret(ctx, location, secretData)
	if createErr != nil {
		return nil, fmt.Errorf("failed to store publisher secret for org %s: %w", ouID, createErr)
	}
	p.logger.Info("Secret stored successfully",
		"ouID", ouID, "secretRefName", secretRefName)

	// Resolve the SecretReference from OpenChoreo to get the actual remoteRef key/property
	resolvedKVPath, resolvedKey, resolveErr := p.resolveSecretRef(ctx, ouID, secretRefName)
	if resolveErr != nil {
		return nil, fmt.Errorf("failed to resolve SecretReference for org %s: %w", ouID, resolveErr)
	}

	// Encrypt the client secret so the scheduler can decrypt and use it for token generation.
	encryptedSecret, err := utils.EncryptBytes([]byte(clientSecret), p.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt publisher secret for org %s: %w", ouID, err)
	}

	// Save to DB — treat as fatal since we just provisioned real credentials
	dbCred := &models.OrgPublisherCredential{
		OUID:                  ouID,
		OrgUUID:               orgUUID,
		ClientID:              clientID,
		SecretKVPath:          resolvedKVPath,
		SecretKey:             resolvedKey,
		ClientSecretEncrypted: encryptedSecret,
	}
	if dbErr := p.credRepo.Upsert(dbCred); dbErr != nil {
		return nil, fmt.Errorf("failed to persist publisher credentials for org %s: %w", ouID, dbErr)
	}

	p.logger.Info("Provisioned new publisher credentials",
		"ouID", ouID, "clientID", clientID, "kvPath", resolvedKVPath, "secretKey", resolvedKey)

	return &PublisherCredentials{
		ClientID:     clientID,
		SecretKVPath: resolvedKVPath,
		SecretKey:    resolvedKey,
	}, nil
}

// provisionSchedulerCredentials provisions the scheduler-only credential, bound to schedulerRoleName; never injected into the eval-job pod.
//
// Request path only — it writes to the secret store, which authenticates with the caller's
// JWT from the request context. See GetOCClientForOrg for why the scheduler cannot call it.
//
// Serialised per org, because the flow rotates the app's client secret in Thunder. That
// rotation is destructive and not idempotent: two overlapping runs leave Thunder holding
// the second rotation while the database records the first, which no later call repairs
// because both paths treat a credential with a secret as already provisioned.
func (p *publisherCredentialProvisioner) provisionSchedulerCredentials(ctx context.Context, ouID, orgUUID string) error {
	_, err, _ := p.sfg.Do("schedulerCred:"+ouID, func() (any, error) {
		return nil, p.doProvisionSchedulerCredentials(ctx, ouID, orgUUID)
	})
	return err
}

func (p *publisherCredentialProvisioner) doProvisionSchedulerCredentials(ctx context.Context, ouID, orgUUID string) error {
	existing, lookupErr := p.schedulerCredRepo.GetByOrgName(ouID)
	switch {
	case lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound):
		return fmt.Errorf("failed to look up scheduler credentials for org %s: %w", ouID, lookupErr)

	case lookupErr == nil && len(existing.ClientSecretEncrypted) > 0:
		p.logger.Debug("Found existing scheduler credentials in DB",
			"ouID", ouID, "clientID", existing.ClientID)

		// Idempotent re-verify; non-fatal if the ClusterAuthzRole isn't installed yet.
		if bindErr := p.ocClient.EnsureClusterRoleBinding(ctx, existing.ClientID, schedulerRoleName); bindErr != nil {
			p.logger.Warn("Failed to ensure ClusterAuthzRoleBinding for existing scheduler credentials",
				"ouID", ouID, "clientID", existing.ClientID, "error", bindErr)
		}
		return nil

	case lookupErr == nil:
		// A row with no usable secret is not "already provisioned": the scheduler cannot mint
		// a token from it and has no way to repair it, so re-provision rather than returning
		// early and leaving the org stuck.
		p.logger.Warn("Scheduler credentials exist without a usable secret, re-provisioning",
			"ouID", ouID, "clientID", existing.ClientID)

	default:
		p.logger.Info("No existing scheduler credentials, provisioning via Thunder", "ouID", ouID)
	}

	appName := "amp-scheduler-" + ouID
	clientID, clientSecret, created, err := p.thunderClient.EnsureApp(ctx, appName, orgUUID)
	if err != nil {
		return fmt.Errorf("failed to provision Thunder scheduler app for org %s: %w", ouID, err)
	}
	p.logger.Info("Thunder EnsureApp result for scheduler credential",
		"ouID", ouID, "clientID", clientID, "created", created, "hasSecret", clientSecret != "")

	if !created && clientSecret == "" {
		p.logger.Warn("Thunder scheduler app exists but secret not available — regenerating client secret",
			"ouID", ouID, "clientID", clientID)

		clientSecret, err = p.thunderClient.RegenerateAppClientSecret(ctx, appName)
		if err != nil {
			return fmt.Errorf("failed to regenerate scheduler client secret for org %s: %w", ouID, err)
		}
		p.logger.Info("Regenerated Thunder scheduler client secret",
			"ouID", ouID, "clientID", clientID)
	}

	if clientSecret == "" {
		return fmt.Errorf("failed to provision scheduler credentials for org %s: no client secret available", ouID)
	}

	encryptedSecret, err := utils.EncryptBytes([]byte(clientSecret), p.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt scheduler secret for org %s: %w", ouID, err)
	}

	// Record the secret Thunder now holds before attempting anything else that can fail.
	// The rotation above is destructive and cannot be undone or replayed, so persisting at
	// the end of the flow means any failure in between strands the org: Thunder has moved
	// to a new secret that nothing holds a copy of, and the scheduler can no longer get a
	// token. Writing here keeps the database and Thunder in agreement no matter where the
	// rest of the flow gives out. The reference fields are filled in by the completing
	// upsert below — nothing reads them for a scheduler credential, which is why they can
	// lag the secret itself.
	dbCred := &models.OrgSchedulerCredential{
		OUID:                  ouID,
		OrgUUID:               orgUUID,
		ClientID:              clientID,
		ClientSecretEncrypted: encryptedSecret,
	}
	if dbErr := p.schedulerCredRepo.Upsert(dbCred); dbErr != nil {
		return fmt.Errorf("failed to persist scheduler credentials for org %s: %w", ouID, dbErr)
	}

	location := schedulerSecretLocation(ouID)
	secretData := map[string]string{
		"client-id":     clientID,
		"client-secret": clientSecret,
	}

	secretRefName, createErr := p.secretClient.CreateSecret(ctx, location, secretData)
	if createErr != nil {
		return fmt.Errorf("failed to store scheduler secret for org %s: %w", ouID, createErr)
	}
	p.logger.Info("Scheduler secret stored successfully",
		"ouID", ouID, "secretRefName", secretRefName)

	resolvedKVPath, resolvedKey, resolveErr := p.resolveSecretRef(ctx, ouID, secretRefName)
	if resolveErr != nil {
		return fmt.Errorf("failed to resolve SecretReference for scheduler credentials of org %s: %w", ouID, resolveErr)
	}

	// ClusterAuthzRoleBindings are cluster-scoped; non-fatal if the role isn't installed yet.
	if bindErr := p.ocClient.EnsureClusterRoleBinding(ctx, clientID, schedulerRoleName); bindErr != nil {
		p.logger.Warn("Failed to ensure ClusterAuthzRoleBinding for new scheduler credentials",
			"ouID", ouID, "clientID", clientID, "role", schedulerRoleName, "error", bindErr)
	} else {
		p.logger.Info("ClusterAuthzRoleBinding ensured for scheduler credential",
			"ouID", ouID, "clientID", clientID, "role", schedulerRoleName)
	}

	dbCred.SecretKVPath = resolvedKVPath
	dbCred.SecretKey = resolvedKey
	if dbErr := p.schedulerCredRepo.Upsert(dbCred); dbErr != nil {
		return fmt.Errorf("failed to persist scheduler credential references for org %s: %w", ouID, dbErr)
	}

	p.logger.Info("Provisioned new scheduler credentials",
		"ouID", ouID, "clientID", clientID, "kvPath", resolvedKVPath, "secretKey", resolvedKey)

	return nil
}

// GetOCClientForOrg returns a cached OC client authenticated with the publisher app's
// org-scoped token. Used by the scheduler for CreateWorkflowRun and GetWorkflowRun —
// operations that run without a live user request context.
//
// The OpenChoreoClient (and the AuthProvider it wraps, plus the underlying http.Client)
// is built once per org and cached, so connection-pool keep-alive and token-refresh state
// are preserved across scheduler cycles.
func (p *publisherCredentialProvisioner) GetOCClientForOrg(ctx context.Context, ouID string) (client.OpenChoreoClient, error) {
	p.orgOCMu.RLock()
	c, ok := p.orgOCClients[ouID]
	p.orgOCMu.RUnlock()
	if ok {
		return c, nil
	}

	result, err, _ := p.sfg.Do("ocClient:"+ouID, func() (any, error) {
		// Re-check under read lock — singleflight may have just finished a previous build.
		p.orgOCMu.RLock()
		if c, ok := p.orgOCClients[ouID]; ok {
			p.orgOCMu.RUnlock()
			return c, nil
		}
		p.orgOCMu.RUnlock()

		// Read-only by design. Provisioning writes to the secret store, and that write
		// authenticates with the caller's JWT taken from the request context — which the
		// scheduler, running on a timer with no inbound request, does not have. Attempting
		// it here fails at the store write every single cycle, but only *after* the Thunder
		// client secret has already been rotated, so each attempt silently invalidates the
		// org's credential and persists nothing. Two overlapping attempts are worse still:
		// whatever the request path just stored is rotated out from under it, leaving the
		// database and Thunder permanently disagreeing.
		//
		// So report the absence and let the request path (monitor creation, which does carry
		// a JWT) be the only thing that provisions or repairs.
		cred, err := p.schedulerCredRepo.GetByOrgName(ouID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w for org %s: credentials are provisioned on the request path, "+
					"so this resolves once a monitor is created in the org", ErrSchedulerCredentialNotFound, ouID)
			}
			return nil, fmt.Errorf("failed to look up scheduler credentials for org %s: %w", ouID, err)
		}
		if len(cred.ClientSecretEncrypted) == 0 {
			return nil, fmt.Errorf("%w for org %s: the stored credential (client %s) has no usable secret "+
				"and can only be repaired on the request path", ErrSchedulerCredentialNotFound, ouID, cred.ClientID)
		}

		secretBytes, err := utils.DecryptBytes(cred.ClientSecretEncrypted, p.encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt publisher secret for org %s: %w", ouID, err)
		}

		authProv := ocauth.NewAuthProvider(ocauth.Config{
			TokenURL:     p.idpTokenURL,
			ClientID:     cred.ClientID,
			ClientSecret: string(secretBytes),
		})
		ocCl, err := client.NewOpenChoreoClient(&client.Config{
			BaseURL:          p.ocBaseURL,
			DefaultNamespace: config.GetConfig().OpenChoreo.DefaultNamespace,
			AuthProvider:     authProv,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to build OC client for org %s: %w", ouID, err)
		}

		p.orgOCMu.Lock()
		p.orgOCClients[ouID] = ocCl
		p.orgOCMu.Unlock()

		p.logger.Debug("Created org OC client", "ouID", ouID, "clientID", cred.ClientID)
		return ocCl, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(client.OpenChoreoClient), nil
}
