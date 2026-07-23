package interceptors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlinasJWTAuthInterceptor_NoOpForOtherAuthTypes(t *testing.T) {
	cases := [][]string{
		nil,
		{"vers=3"},
		{"authType=rrsa"},
		{"authType="},
		{"tls,vers=3"}, // compound, no authType
	}
	for _, opts := range cases {
		called := false
		op := &mounter.MountOperation{Options: opts}
		err := AlinasJWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
			called = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, called, "handler should be invoked for opts %v", opts)
	}
}

func TestAlinasJWTAuthInterceptor_NilOp(t *testing.T) {
	called := false
	err := AlinasJWTAuthInterceptor(context.Background(), nil, func(ctx context.Context, o *mounter.MountOperation) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestAlinasJWTAuthInterceptor_ConfigError(t *testing.T) {
	// authType set but missing sandboxId/credProvider -> validate fails,
	// handler must not run.
	called := false
	op := &mounter.MountOperation{Options: []string{"authType=jwtauth"}}
	err := AlinasJWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwtauth config error")
	assert.False(t, called)
}

func TestAlinasJWTAuthInterceptor_FetchFailFast(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	called := false
	op := &mounter.MountOperation{
		Options: []string{
			"authType=jwtauth",
			"sandboxId=sb-1",
			"sandboxCredProviderName=cp",
			"jwtauth_endpoint=" + srv.URL,
			"jwtauth_token_file=" + tokenPath,
		},
	}
	err := AlinasJWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch STS token")
	assert.False(t, called, "mount must not proceed when STS acquisition fails")
}

func TestAlinasJWTAuthInterceptor_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(credentialResponse{
			RequestID: "r1",
			STSToken: &stsToken{
				AccessKeyID: "AKID", AccessKeySecret: "AKSECRET", SecurityToken: "STOKEN",
				Expiration: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()

	// Mix compound and single options, plus a stale static credential that must
	// be stripped and a pre-existing tls that must not be duplicated.
	op := &mounter.MountOperation{
		Target: "/mnt/nas",
		Options: []string{
			"tls,vers=3,authType=jwtauth",
			"sandboxId=sb-1",
			"sandboxCredProviderName=cp",
			"jwtauth_endpoint=" + srv.URL,
			"jwtauth_token_file=" + tokenPath,
			"access_key_id=STALE",
			"ro",
		},
	}

	var seen map[string]string
	err := AlinasJWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
		seen = mounterutils.IndexMountOptions(o.Options)
		return nil
	})
	require.NoError(t, err)

	// STS triple injected.
	assert.Equal(t, "AKID", seen[optAlinasAccessKeyID])
	assert.Equal(t, "AKSECRET", seen[optAlinasAccessKeySecret])
	assert.Equal(t, "STOKEN", seen[optAlinasSecurityToken])
	// Stale static AK stripped (replaced by STS value, not "STALE").
	assert.NotEqual(t, "STALE", seen[optAlinasAccessKeyID])
	// Infra-only jwtauth options removed.
	for _, k := range []string{optSandboxId, optSandboxCredProviderName,
		optJWTAuthEndpoint, optJWTAuthTokenFile} {
		_, ok := seen[k]
		assert.False(t, ok, "expected %s to be removed", k)
	}
	// Preserved, non-credential options.
	assert.Equal(t, "3", seen["vers"])
	_, hasRo := seen["ro"]
	assert.True(t, hasRo)
	// tls preserved (not duplicated).
	_, hasTLS := seen[optAlinasTLS]
	assert.True(t, hasTLS)
	tlsCount := 0
	for _, o := range op.Options {
		if o == optAlinasTLS {
			tlsCount++
		}
	}
	assert.Equal(t, 1, tlsCount, "tls must not be duplicated")
	// authType preserved so downstream can branch.
	assert.Equal(t, AuthTypeJWTAuth, seen[optAuthType])
}

func TestInjectAlinasSTSOptions_AddsTLSWhenMissing(t *testing.T) {
	cred := &stsToken{AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "st"}
	out := injectAlinasSTSOptions([]string{"vers=3"}, cred)
	idx := mounterutils.IndexMountOptions(out)
	_, hasTLS := idx[optAlinasTLS]
	assert.True(t, hasTLS, "tls should be added when absent")
	assert.Equal(t, "ak", idx[optAlinasAccessKeyID])
	assert.Equal(t, "sk", idx[optAlinasAccessKeySecret])
	assert.Equal(t, "st", idx[optAlinasSecurityToken])
}

func TestFlattenMountOptions(t *testing.T) {
	in := []string{"tls,vers=3", "authType=jwtauth", "", "ro,,nolock"}
	out := flattenMountOptions(in)
	assert.Equal(t, []string{"tls", "vers=3", "authType=jwtauth", "ro", "nolock"}, out)
}
