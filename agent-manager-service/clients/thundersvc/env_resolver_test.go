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

package thundersvc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixed ouID/orgNamespace used by most tests below, kept deliberately distinct
// from each other so a test that accidentally swaps the two arguments fails
// loudly instead of passing by coincidence.
const (
	testOUID         = "ou-acme"
	testOrgNamespace = "default"
)

// okReader returns a fixed clientID/secret for any (ouID, env) — the common
// case for tests that don't care about the read itself.
func okReader(clientID, secret string) ReadSystemClientFunc {
	return func(context.Context, string, string) (string, string, error) {
		return clientID, secret, nil
	}
}

// fakeResolveBaseURL stands in for real network probing — always reports a
// fake, reachable base URL with no dial override.
func fakeResolveBaseURL(_ context.Context, _, _, _ string) (string, string, bool) {
	return "http://fake-thunder:8090", "", true
}

// noHandleReader stands in for an environment that was never provisioned
// through add-environment-thunder.sh (or whose registration never completed):
// no URL handle exists, so Resolve must report ErrThunderNotProvisioned — there
// is no fallback to compute an address from org/env.
func noHandleReader(context.Context, string, string) (string, error) {
	return "", nil
}

// okHandleReader returns a fixed handle for any (ouID, env) — the common case
// for tests that don't care about the handle lookup itself, just that Resolve
// gets past it.
func okHandleReader(handle string) ReadThunderHandleFunc {
	return func(context.Context, string, string) (string, error) {
		return handle, nil
	}
}

func TestEnvThunderResolver_Resolve_Success(t *testing.T) {
	var gotOUID, gotEnv string
	read := func(_ context.Context, ouID, env string) (string, string, error) {
		gotOUID, gotEnv = ouID, env
		return "amp-system-client", "the-system-client-secret", nil
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	client, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, testOUID, gotOUID, "the credential read must be scoped by ouID, not orgNamespace")
	assert.Equal(t, "staging", gotEnv)
}

// TestEnvThunderResolver_Resolve_UsesStoredClientID confirms the resolver uses
// the client ID returned by the reader (not only the well-known constant).
func TestEnvThunderResolver_Resolve_UsesStoredClientID(t *testing.T) {
	resolver := newEnvThunderResolverWithReader(okReader("custom-client-id", "s3cr3t"), okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	client, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	tc, ok := client.(*thunderClient)
	require.True(t, ok)
	assert.Equal(t, "custom-client-id", tc.clientID)
}

// TestEnvThunderResolver_Resolve_RejectsPathBreakingSegments guards ouID/orgNamespace/env
// with a clear, fast error rather than letting a nonsensical value reach the reader.
func TestEnvThunderResolver_Resolve_RejectsPathBreakingSegments(t *testing.T) {
	read := func(context.Context, string, string) (string, string, error) {
		t.Fatal("must not read the store when a segment is invalid")
		return "", "", nil // unreachable — t.Fatal above halts the test
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	cases := []struct{ ouID, ns, env string }{
		{"..", testOrgNamespace, "staging"},
		{testOUID, "..", "staging"},
		{testOUID, testOrgNamespace, ".."},
		{".", testOrgNamespace, "staging"},
		{testOUID, ".", "staging"},
		{testOUID, testOrgNamespace, "."},
		{"", testOrgNamespace, "staging"},
		{testOUID, "", "staging"},
		{testOUID, testOrgNamespace, ""},
		{"acme/evil", testOrgNamespace, "staging"},
	}
	for _, tc := range cases {
		_, err := resolver.Resolve(context.Background(), tc.ouID, tc.ns, tc.env)
		require.Error(t, err, "ouID=%q ns=%q env=%q", tc.ouID, tc.ns, tc.env)
	}
}

func TestEnvThunderResolver_Resolve_Caches(t *testing.T) {
	calls := 0
	read := func(context.Context, string, string) (string, string, error) {
		calls++
		return "amp-system-client", "s3cr3t", nil
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	c1, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	c2, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)

	assert.Same(t, c1, c2, "a resolved client for the same ouID/namespace/env must be cached, not rebuilt")
	assert.Equal(t, 1, calls, "the secret must only be fetched once per ouID/env")
}

// TestEnvThunderResolver_Resolve_ExpiresAfterTTL guards against a cached client
// outliving a secret rotation — without the TTL, a rotated secret would break until restart.
func TestEnvThunderResolver_Resolve_ExpiresAfterTTL(t *testing.T) {
	calls := 0
	read := func(context.Context, string, string) (string, string, error) {
		calls++
		return "amp-system-client", "s3cr3t", nil
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), fakeResolveBaseURL)
	now := time.Now()
	resolver.now = func() time.Time { return now }
	resolver.ttl = time.Minute

	c1, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)

	now = now.Add(30 * time.Second)
	c2, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	assert.Same(t, c1, c2, "still within TTL, must not rebuild")
	assert.Equal(t, 1, calls)

	now = now.Add(time.Minute)
	c3, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	assert.NotSame(t, c1, c3, "past TTL, must re-read the store and rebuild so a rotated secret takes effect")
	assert.Equal(t, 2, calls)
}

// TestEnvThunderResolver_Resolve_ConcurrentCacheMiss_DedupesViaSingleflight guards
// against a thundering-herd: concurrent first-time resolves must share one read/probe.
func TestEnvThunderResolver_Resolve_ConcurrentCacheMiss_DedupesViaSingleflight(t *testing.T) {
	var readCalls, probeCalls int64
	read := func(context.Context, string, string) (string, string, error) {
		atomic.AddInt64(&readCalls, 1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		return "amp-system-client", "s3cr3t", nil
	}
	probeFn := func(_ context.Context, _, _, _ string) (string, string, bool) {
		atomic.AddInt64(&probeCalls, 1)
		return "http://fake-thunder:8090", "", true
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), probeFn)

	const goroutines = 20
	clients := make([]ThunderClient, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			c, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
			errs[idx] = err
			clients[idx] = c
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d", i)
	}
	assert.EqualValues(t, 1, atomic.LoadInt64(&readCalls), "concurrent cache misses for the same key must share one credential read")
	assert.EqualValues(t, 1, atomic.LoadInt64(&probeCalls), "concurrent cache misses for the same key must share one base-URL probe")
	for i := 1; i < goroutines; i++ {
		assert.Same(t, clients[0], clients[i], "all concurrent resolvers for the same key must get the identical client")
	}
}

func TestEnvThunderResolver_Resolve_DifferentEnvironmentsAreNotCachedTogether(t *testing.T) {
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", "s3cr3t"), okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	staging, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)
	prod, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "prod")
	require.NoError(t, err)

	assert.NotSame(t, staging, prod)
}

// TestEnvThunderResolver_Resolve_DifferentOUIDsAreNotCachedTogether is the
// multi-tenant-safety property this resolver split exists for: two tenants
// hitting the same env name must never share a cached client (or, upstream of
// caching, each other's credential row).
func TestEnvThunderResolver_Resolve_DifferentOUIDsAreNotCachedTogether(t *testing.T) {
	var gotOUIDs []string
	read := func(_ context.Context, ouID, _ string) (string, string, error) {
		gotOUIDs = append(gotOUIDs, ouID)
		return "amp-system-client", "s3cr3t-" + ouID, nil
	}
	resolver := newEnvThunderResolverWithReader(read, okHandleReader("x7f2q9kz"), fakeResolveBaseURL)

	acme, err := resolver.Resolve(context.Background(), "ou-acme", testOrgNamespace, "staging")
	require.NoError(t, err)
	other, err := resolver.Resolve(context.Background(), "ou-other", testOrgNamespace, "staging")
	require.NoError(t, err)

	assert.NotSame(t, acme, other, "same orgNamespace/env but different ouID must resolve to different clients")
	assert.Equal(t, []string{"ou-acme", "ou-other"}, gotOUIDs, "each ouID must trigger its own credential read")
}

func TestEnvThunderResolver_Resolve_NotProvisioned_NoRow(t *testing.T) {
	read := func(context.Context, string, string) (string, string, error) {
		return "", "", ErrThunderNotProvisioned
	}
	resolver := newEnvThunderResolverWithReader(read, noHandleReader, fakeResolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "no-such-env")
	assert.True(t, errors.Is(err, ErrThunderNotProvisioned))
}

func TestEnvThunderResolver_Resolve_NotProvisioned_EmptySecret(t *testing.T) {
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", ""), noHandleReader, fakeResolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "half-provisioned-env")
	assert.True(t, errors.Is(err, ErrThunderNotProvisioned))
}

// TestEnvThunderResolver_Resolve_NoHandleRegistered locks in the core guarantee
// of the security fix: a missing URL handle is treated as not-provisioned, with
// NO fallback to resolveBaseURL (and so no address computed from org/env) — even
// though the system-client secret read succeeds. resolveBaseURL must never even
// be called.
func TestEnvThunderResolver_Resolve_NoHandleRegistered(t *testing.T) {
	resolveBaseURLCalled := false
	resolveBaseURL := func(context.Context, string, string, string) (string, string, bool) {
		resolveBaseURLCalled = true
		return "", "", false
	}
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", "s3cr3t"), noHandleReader, resolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "never-registered-env")
	assert.True(t, errors.Is(err, ErrThunderNotProvisioned), "no handle must be treated as not-provisioned")
	assert.False(t, resolveBaseURLCalled, "must short-circuit before ever attempting to resolve a base URL")
}

func TestEnvThunderResolver_Resolve_HandleReadErrorPropagates(t *testing.T) {
	dbErr := errors.New("db unavailable")
	readHandle := func(context.Context, string, string) (string, error) {
		return "", dbErr
	}
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", "s3cr3t"), readHandle, fakeResolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrThunderNotProvisioned), "database read error must propagate and not be misclassified as ErrThunderNotProvisioned")
	assert.True(t, errors.Is(err, dbErr), "underlying db error must be wrapped in error chain")
}

func TestEnvThunderResolver_Resolve_ReadErrorPropagates(t *testing.T) {
	boom := errors.New("decrypt failed")
	read := func(context.Context, string, string) (string, string, error) {
		return "", "", boom
	}
	resolver := newEnvThunderResolverWithReader(read, noHandleReader, fakeResolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrThunderNotProvisioned), "a real read error must not be mistaken for not-provisioned")
}

func TestEnvThunderResolver_Resolve_UsesResolvedBaseURLAndDialOverride(t *testing.T) {
	var gotNamespace, gotEnv string
	resolveBaseURL := func(_ context.Context, ns, env, _ string) (string, string, bool) {
		gotNamespace, gotEnv = ns, env
		return "http://acme-staging.amp.localhost:8080", "host.docker.internal:8080", true
	}
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", "s3cr3t"), okHandleReader("x7f2q9kz"), resolveBaseURL)

	client, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.NoError(t, err)

	assert.Equal(t, testOrgNamespace, gotNamespace, "the base URL must be built from orgNamespace, not ouID")
	assert.Equal(t, "staging", gotEnv)

	tc, ok := client.(*thunderClient)
	require.True(t, ok)
	assert.Equal(t, "http://acme-staging.amp.localhost:8080", tc.baseURL)
	require.NotNil(t, tc.httpClient.Transport, "a non-empty dial override must install a custom transport")
}

func TestEnvThunderResolver_Resolve_ThunderUnreachable(t *testing.T) {
	resolveBaseURL := func(_ context.Context, _, _, _ string) (string, string, bool) {
		return "", "", false
	}
	resolver := newEnvThunderResolverWithReader(okReader("amp-system-client", "s3cr3t"), okHandleReader("x7f2q9kz"), resolveBaseURL)

	_, err := resolver.Resolve(context.Background(), testOUID, testOrgNamespace, "staging")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrThunderUnreachable))
	assert.False(t, errors.Is(err, ErrThunderNotProvisioned), "unreachable-but-provisioned must not be treated as never-provisioned (that classifies as a permanent failure upstream, unreachable must be retried)")
}
