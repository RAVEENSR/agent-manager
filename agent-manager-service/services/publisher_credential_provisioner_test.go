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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	occlient "github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/clients/secretmanagersvc"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

var testEncryptionKey = []byte("01234567890123456789012345678901") // 32 bytes for AES-256

func newTestSecretRef(kvPath, property string) *occlient.SecretReferenceInfo {
	return &occlient.SecretReferenceInfo{
		Name: "some-secret-ref",
		Data: []occlient.SecretDataSourceInfo{
			{SecretKey: "client-secret", RemoteRef: occlient.RemoteRefInfo{Key: kvPath, Property: property}},
		},
	}
}

// Regression guard: only the scheduler credential is bound to amp-monitor-scheduler, never the publisher one.
func TestEnsureCredentials_NewOrg_BindsOnlySchedulerCredential(t *testing.T) {
	var boundClientIDs []string
	var boundRoles []string

	thunderMock := &clientmocks.ThunderClientMock{
		EnsurePublisherAppFunc: func(_ context.Context, orgName, _ string) (string, string, bool, error) {
			return "amp-publisher-" + orgName, "publisher-secret", true, nil
		},
		EnsureAppFunc: func(_ context.Context, appName, _ string) (string, string, bool, error) {
			return appName, "scheduler-secret", true, nil
		},
	}

	ocMock := &clientmocks.OpenChoreoClientMock{
		EnsureClusterRoleBindingFunc: func(_ context.Context, clientID string, roleName string) error {
			boundClientIDs = append(boundClientIDs, clientID)
			boundRoles = append(boundRoles, roleName)
			return nil
		},
		GetSecretReferenceFunc: func(_ context.Context, _ string, secretRefName string) (*occlient.SecretReferenceInfo, error) {
			return newTestSecretRef("kv/"+secretRefName, "client-secret"), nil
		},
	}

	var publisherUpserts []*models.OrgPublisherCredential
	credRepo := &repomocks.OrgPublisherCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgPublisherCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
		UpsertFunc: func(cred *models.OrgPublisherCredential) error {
			publisherUpserts = append(publisherUpserts, cred)
			return nil
		},
	}

	var schedulerUpserts []models.OrgSchedulerCredential
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
		UpsertFunc: func(cred *models.OrgSchedulerCredential) error {
			// Snapshot by value: the caller reuses one struct across both writes, so
			// storing the pointer would make every assertion read post-mutation state.
			schedulerUpserts = append(schedulerUpserts, *cred)
			return nil
		},
	}

	secretClient := &clientmocks.SecretManagementClientMock{
		CreateSecretFunc: func(_ context.Context, location secretmanagersvc.SecretLocation, _ map[string]string) (string, error) {
			return "ref-" + location.EntityName, nil
		},
	}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		secretClient:      secretClient,
		ocClient:          ocMock,
		credRepo:          credRepo,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	creds, err := p.EnsureCredentials(context.Background(), "acme", "org-uuid-1")
	require.NoError(t, err)
	assert.Equal(t, "amp-publisher-acme", creds.ClientID)

	require.Len(t, publisherUpserts, 1)
	assert.Equal(t, "amp-publisher-acme", publisherUpserts[0].ClientID)

	// Two upserts: the secret is recorded as soon as it is known, then the SecretReference
	// fields are filled in once resolved. See provisionSchedulerCredentials for why.
	require.Len(t, schedulerUpserts, 2)
	assert.Equal(t, "amp-scheduler-acme", schedulerUpserts[0].ClientID)
	assert.NotEmpty(t, schedulerUpserts[0].ClientSecretEncrypted,
		"the first write must already carry the secret")
	assert.Empty(t, schedulerUpserts[0].SecretKVPath,
		"the first write lands before the reference fields are resolved")
	assert.NotEmpty(t, schedulerUpserts[1].SecretKVPath)
	assert.NotEmpty(t, schedulerUpserts[1].SecretKey)

	require.Len(t, boundClientIDs, 1, "EnsureClusterRoleBinding should be called exactly once")
	assert.Equal(t, "amp-scheduler-acme", boundClientIDs[0])
	assert.Equal(t, schedulerRoleName, boundRoles[0])
}

// Idempotent path: only the scheduler credential's binding is re-verified on repeat calls.
func TestEnsureCredentials_ExistingOrg_ReverifiesOnlySchedulerBinding(t *testing.T) {
	var boundClientIDs []string

	// A healthy row carries a usable secret; without one it would be re-provisioned instead.
	schedulerSecret, err := utils.EncryptBytes([]byte("scheduler-secret-value"), testEncryptionKey)
	require.NoError(t, err)

	// Empty: re-verifying an existing credential must not touch Thunder.
	thunderMock := &clientmocks.ThunderClientMock{}
	ocMock := &clientmocks.OpenChoreoClientMock{
		EnsureClusterRoleBindingFunc: func(_ context.Context, clientID string, _ string) error {
			boundClientIDs = append(boundClientIDs, clientID)
			return nil
		},
	}

	credRepo := &repomocks.OrgPublisherCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgPublisherCredential, error) {
			return &models.OrgPublisherCredential{
				ClientID: "amp-publisher-acme", SecretKVPath: "kv/publisher", SecretKey: "client-secret",
			}, nil
		},
	}
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return &models.OrgSchedulerCredential{
				ClientID: "amp-scheduler-acme", SecretKVPath: "kv/scheduler", SecretKey: "client-secret",
				ClientSecretEncrypted: schedulerSecret,
			}, nil
		},
	}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		ocClient:          ocMock,
		credRepo:          credRepo,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	creds, err := p.EnsureCredentials(context.Background(), "acme", "org-uuid-1")
	require.NoError(t, err)
	assert.Equal(t, "amp-publisher-acme", creds.ClientID)

	require.Len(t, boundClientIDs, 1, "only the scheduler credential's binding should be re-verified")
	assert.Equal(t, "amp-scheduler-acme", boundClientIDs[0])
}

// credRepo has no GetByOrgNameFunc — the mock panics if GetOCClientForOrg ever touches it.
func TestGetOCClientForOrg_ReadsFromSchedulerCredRepo(t *testing.T) {
	encryptedSecret, err := utils.EncryptBytes([]byte("scheduler-secret-value"), testEncryptionKey)
	require.NoError(t, err)

	credRepo := &repomocks.OrgPublisherCredentialRepositoryMock{} // no GetByOrgNameFunc — must not be called
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(ouID string) (*models.OrgSchedulerCredential, error) {
			assert.Equal(t, "acme", ouID)
			return &models.OrgSchedulerCredential{
				ClientID:              "amp-scheduler-acme",
				ClientSecretEncrypted: encryptedSecret,
			}, nil
		},
	}

	p := &publisherCredentialProvisioner{
		credRepo:          credRepo,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		idpTokenURL:       "http://thunder.test/oauth2/token",
		ocBaseURL:         "http://openchoreo.test",
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	ocClient, err := p.GetOCClientForOrg(context.Background(), "acme")
	require.NoError(t, err)
	assert.NotNil(t, ocClient)
}

// Provisioning writes to the secret store, and that write authenticates with a JWT taken
// from the request context. The scheduler has no request context, so provisioning from
// here could only ever fail — after it had already rotated the org's Thunder secret. Fail
// fast on a sentinel instead, and touch neither Thunder nor the secret store.
func TestGetOCClientForOrg_MissingSchedulerCred_DoesNotProvision(t *testing.T) {
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
		// No UpsertFunc: the mock panics if anything tries to persist from this path.
	}
	// Every mock below is deliberately empty — any call panics the test.
	thunderMock := &clientmocks.ThunderClientMock{}
	secretClient := &clientmocks.SecretManagementClientMock{}
	ocMock := &clientmocks.OpenChoreoClientMock{}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		secretClient:      secretClient,
		ocClient:          ocMock,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		idpTokenURL:       "http://thunder.test/oauth2/token",
		ocBaseURL:         "http://openchoreo.test",
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	_, err := p.GetOCClientForOrg(context.Background(), "acme")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSchedulerCredentialNotFound)
	assert.Empty(t, p.orgOCClients, "nothing should be cached for an unprovisioned org")
}

// Same reasoning for a row that exists but carries no usable secret: repairing it means
// rotating in Thunder and writing the secret store, so the scheduler must not attempt it.
func TestGetOCClientForOrg_EmptyEncryptedSecret_DoesNotRepair(t *testing.T) {
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return &models.OrgSchedulerCredential{
				ClientID:              "amp-scheduler-acme",
				ClientSecretEncrypted: nil,
			}, nil
		},
	}
	thunderMock := &clientmocks.ThunderClientMock{}
	secretClient := &clientmocks.SecretManagementClientMock{}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		secretClient:      secretClient,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		idpTokenURL:       "http://thunder.test/oauth2/token",
		ocBaseURL:         "http://openchoreo.test",
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	_, err := p.GetOCClientForOrg(context.Background(), "acme")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSchedulerCredentialNotFound)
}

// A real DB error must not be reported as "genuinely absent".
func TestGetOCClientForOrg_RealDBError_NotMisreportedAsNotFound(t *testing.T) {
	dbTimeout := errors.New("db timeout")
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return nil, dbTimeout
		},
	}

	p := &publisherCredentialProvisioner{
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	_, err := p.GetOCClientForOrg(context.Background(), "acme")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrSchedulerCredentialNotFound),
		"a real DB error must not be reported as the not-found sentinel")
	assert.ErrorIs(t, err, dbTimeout)
}

// Rotating the Thunder secret is destructive and irreversible, while every step after it
// can fail. If the encrypted secret were only persisted at the end, a failure in between
// would leave Thunder holding a secret nothing has a record of — which is how an org ends
// up permanently unable to mint a token.
func TestProvisionSchedulerCredentials_PersistsSecretBeforeFallibleSteps(t *testing.T) {
	storeDown := errors.New("no JWT token found in context")

	var upserts []*models.OrgSchedulerCredential
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
		UpsertFunc: func(cred *models.OrgSchedulerCredential) error {
			upserts = append(upserts, cred)
			return nil
		},
	}
	thunderMock := &clientmocks.ThunderClientMock{
		EnsureAppFunc: func(_ context.Context, appName, _ string) (string, string, bool, error) {
			return appName, "", false, nil // app exists, secret unavailable → forces a rotation
		},
		RegenerateAppClientSecretFunc: func(_ context.Context, _ string) (string, error) {
			return "rotated-secret", nil
		},
	}
	secretClient := &clientmocks.SecretManagementClientMock{
		CreateSecretFunc: func(_ context.Context, _ secretmanagersvc.SecretLocation, _ map[string]string) (string, error) {
			return "", storeDown
		},
	}
	ocMock := &clientmocks.OpenChoreoClientMock{}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		secretClient:      secretClient,
		ocClient:          ocMock,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	err := p.provisionSchedulerCredentials(context.Background(), "acme", "org-uuid-1")
	require.Error(t, err, "the secret-store failure must still surface")
	assert.ErrorIs(t, err, storeDown)

	require.NotEmpty(t, upserts, "the rotated secret must be recorded before the store write")
	decrypted, decErr := utils.DecryptBytes(upserts[0].ClientSecretEncrypted, testEncryptionKey)
	require.NoError(t, decErr)
	assert.Equal(t, "rotated-secret", string(decrypted),
		"the persisted secret must match what Thunder now holds")
}

// Provisioning rotates a secret in Thunder, so running it twice over for one org leaves
// Thunder holding the second rotation while the DB records the first. Whatever the entry
// point, concurrent provisioning of the same org must collapse to a single run.
func TestProvisionSchedulerCredentials_ConcurrentCallsForOneOrgCollapse(t *testing.T) {
	var mu sync.Mutex
	rotations := 0

	// EnsureApp signals on every entry and then parks, so while the first call is parked a
	// second signal can only mean a second call got through. Waiting for that signal, rather
	// than sleeping and hoping the second call got far enough, inverts the timing risk: a
	// starved runner can make this test pass spuriously but never fail spuriously.
	entries := make(chan struct{}, 4)
	release := make(chan struct{})

	thunderMock := &clientmocks.ThunderClientMock{
		EnsureAppFunc: func(_ context.Context, appName, _ string) (string, string, bool, error) {
			entries <- struct{}{}
			<-release
			return appName, "", false, nil // app exists, secret unavailable → forces a rotation
		},
		RegenerateAppClientSecretFunc: func(_ context.Context, _ string) (string, error) {
			mu.Lock()
			rotations++
			mu.Unlock()
			return "rotated-secret", nil
		},
	}
	schedulerCredRepo := &repomocks.OrgSchedulerCredentialRepositoryMock{
		GetByOrgNameFunc: func(_ string) (*models.OrgSchedulerCredential, error) {
			return nil, gorm.ErrRecordNotFound
		},
		UpsertFunc: func(_ *models.OrgSchedulerCredential) error { return nil },
	}
	ocMock := &clientmocks.OpenChoreoClientMock{
		EnsureClusterRoleBindingFunc: func(_ context.Context, _ string, _ string) error { return nil },
		GetSecretReferenceFunc: func(_ context.Context, _ string, secretRefName string) (*occlient.SecretReferenceInfo, error) {
			return newTestSecretRef("kv/"+secretRefName, "client-secret"), nil
		},
	}
	secretClient := &clientmocks.SecretManagementClientMock{
		CreateSecretFunc: func(_ context.Context, location secretmanagersvc.SecretLocation, _ map[string]string) (string, error) {
			return "ref-" + location.EntityName, nil
		},
	}

	p := &publisherCredentialProvisioner{
		thunderClient:     thunderMock,
		secretClient:      secretClient,
		ocClient:          ocMock,
		schedulerCredRepo: schedulerCredRepo,
		logger:            discardLogger(),
		encryptionKey:     testEncryptionKey,
		orgOCClients:      make(map[string]occlient.OpenChoreoClient),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		assert.NoError(t, p.provisionSchedulerCredentials(context.Background(), "acme", "org-uuid-1"))
	}()

	<-entries // the first call is parked inside EnsureApp and holds the org's slot

	wg.Add(1)
	go func() {
		defer wg.Done()
		assert.NoError(t, p.provisionSchedulerCredentials(context.Background(), "acme", "org-uuid-1"))
	}()

	select {
	case <-entries:
		close(release)
		wg.Wait()
		t.Fatal("the second call reached Thunder while the first was still in flight: " +
			"provisioning for one org was not deduplicated")
	case <-time.After(500 * time.Millisecond):
		// No second entry while the first is parked — the second call was deduplicated.
	}

	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, rotations, "concurrent provisioning of one org must rotate its secret once")
	assert.Empty(t, entries, "EnsureApp must be reached exactly once for one org")
}
