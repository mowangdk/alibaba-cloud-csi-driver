package interceptors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/proxy/server"
	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTAuthInterceptor_NoOpForOtherAuthTypes(t *testing.T) {
	cases := [][]string{
		nil,
		{"bucket=b"},
		{"authType=rrsa"},
		{"authType="},
	}
	for _, opts := range cases {
		called := false
		op := &mounter.MountOperation{Options: opts}
		err := JWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
			called = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, called, "handler should be invoked for opts %v", opts)
	}
}

func TestJWTAuthInterceptor_NilOp(t *testing.T) {
	called := false
	err := JWTAuthInterceptor(context.Background(), nil, func(ctx context.Context, o *mounter.MountOperation) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestResolveJWTAuthOpts_Defaults(t *testing.T) {
	idx := map[string]string{
		optAuthType:                "jwtauth",
		optSandboxId:               "sb-123",
		optSandboxCredProviderName: "my-cred",
	}
	opts := resolveJWTAuthOpts(idx)
	assert.Equal(t, "sb-123", opts.SandboxId)
	assert.Equal(t, "my-cred", opts.CredProvider)
	assert.Equal(t, server.GetJWTAuthTokenFilePath("sb-123"), opts.TokenFile)
	assert.Equal(t, server.GetJWTAuthEndpoint(), opts.Endpoint)
}

func TestResolveJWTAuthOpts_ExplicitOverrides(t *testing.T) {
	idx := map[string]string{
		optSandboxId:           "sb-1",
		optJWTAuthEndpoint:     "https://custom:9443/",
		optJWTAuthTokenFile:    "/custom/token",
		optJWTAuthCredProvider: "explicit-cred",
	}
	opts := resolveJWTAuthOpts(idx)
	assert.Equal(t, "https://custom:9443/", opts.Endpoint)
	assert.Equal(t, "/custom/token", opts.TokenFile)
	assert.Equal(t, "explicit-cred", opts.CredProvider)
}

func TestRewriteJWTOptions(t *testing.T) {
	in := []string{
		"bucket=b",
		"authType=jwtauth",
		"sandboxId=sb-1",
		"sandboxCredProviderName=cp",
		"jwtauth_endpoint=https://x",
		"jwtauth_token_file=/tok",
		"jwtauth_cred_provider=cp",
		"jwtauth_ca_file=/ca",
		"ro",
	}
	out := rewriteJWTAuthOptions(in, "/var/run/nas/credentials/sb-1/sts")
	idx := mounterutils.IndexMountOptions(out)

	// Infra-only keys removed.
	for _, k := range []string{optSandboxId, optSandboxCredProviderName,
		optJWTAuthEndpoint, optJWTAuthTokenFile,
		optJWTAuthCredProvider, optJWTAuthCAFile} {
		_, ok := idx[k]
		assert.False(t, ok, "expected %s to be removed", k)
	}
	// Preserved.
	assert.Equal(t, "b", idx["bucket"])
	assert.Equal(t, "jwtauth", idx[optAuthType])
	_, hasRo := idx["ro"]
	assert.True(t, hasRo)
	// Added.
	assert.Equal(t, "/var/run/nas/credentials/sb-1/sts", idx[optCredentialDir])
}

func TestJWTAuthInterceptor_ConfigError(t *testing.T) {
	// authType set but missing sandboxId/credProvider -> validate fails,
	// handler must not run.
	called := false
	op := &mounter.MountOperation{Options: []string{"authType=jwtauth"}}
	err := JWTAuthInterceptor(context.Background(), op, func(ctx context.Context, o *mounter.MountOperation) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwtauth config error")
	assert.False(t, called)
}

func TestJWTAuthInterceptor_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(credentialResponse{
			RequestID: "r1",
			STSToken: &stsToken{
				AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "st",
				Expiration: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()

	exitCh := make(chan error, 1)
	var credDir string
	op := &mounter.MountOperation{
		VolumeID: "vol-e2e",
		Options: []string{
			"authType=jwtauth",
			"sandboxId=sb-e2e",
			"sandboxCredProviderName=cp",
			"jwtauth_endpoint=" + srv.URL,
			"jwtauth_token_file=" + tokenPath,
			"jwtauth_cred_provider=cp",
		},
	}

	handler := func(ctx context.Context, o *mounter.MountOperation) error {
		// The entrypoint should only see credentialDir + authType.
		idx := mounterutils.IndexMountOptions(o.Options)
		credDir = idx[optCredentialDir]
		require.NotEmpty(t, credDir)
		assert.Equal(t, "jwtauth", idx[optAuthType])
		_, hasSandbox := idx[optSandboxId]
		assert.False(t, hasSandbox)

		// Credentials must exist before handler returns.
		data, err := os.ReadFile(filepath.Join(credDir, mounterutils.KeyAccessKeyId))
		require.NoError(t, err)
		assert.Equal(t, "ak", string(data))

		o.MountResult = server.OssfsMountResult{PID: 1234, ExitChan: exitCh}
		return nil
	}

	err := JWTAuthInterceptor(context.Background(), op, handler)
	require.NoError(t, err)

	// Simulate process exit -> refresher stopped and files cleaned up.
	close(exitCh)
	assert.Eventually(t, func() bool {
		_, statErr := os.Stat(credDir)
		return os.IsNotExist(statErr)
	}, 3*time.Second, 20*time.Millisecond, "credential dir should be cleaned up after exit")
}

func TestJWTAuthInterceptor_HandlerErrorCleansUp(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(credentialResponse{
			RequestID: "r1",
			STSToken: &stsToken{
				AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "st",
				Expiration: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()

	var credDir string
	op := &mounter.MountOperation{
		VolumeID: "vol-handler-err",
		Options: []string{
			"authType=jwtauth",
			"sandboxId=sb-err",
			"sandboxCredProviderName=cp",
			"jwtauth_endpoint=" + srv.URL,
			"jwtauth_token_file=" + tokenPath,
			"jwtauth_cred_provider=cp",
		},
	}

	handlerErr := errors.New("mount failed")
	handler := func(ctx context.Context, o *mounter.MountOperation) error {
		idx := mounterutils.IndexMountOptions(o.Options)
		credDir = idx[optCredentialDir]
		require.NotEmpty(t, credDir)

		// Credentials were written by Start before the handler ran.
		_, err := os.Stat(filepath.Join(credDir, mounterutils.KeyAccessKeyId))
		require.NoError(t, err)
		return handlerErr
	}

	err := JWTAuthInterceptor(context.Background(), op, handler)
	require.ErrorIs(t, err, handlerErr)

	// A failed mount must not leave STS files on disk.
	_, statErr := os.Stat(credDir)
	assert.True(t, os.IsNotExist(statErr), "credential dir should be removed after handler error")
}
