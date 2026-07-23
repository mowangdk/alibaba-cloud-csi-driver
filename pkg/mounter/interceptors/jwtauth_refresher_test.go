package interceptors

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTokenFile(t *testing.T, dir, accessToken, sandboxClientID string) string {
	t.Helper()
	tokenPath := filepath.Join(dir, "token.json")
	content := tokenFileContent{
		RequestID:             "req-1",
		AccessToken:           accessToken,
		SandboxClientID:       sandboxClientID,
		AccessTokenExpiration: time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	data, err := json.Marshal(content)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenPath, data, 0600))
	return tokenPath
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func newTestRefresher(tokenPath, endpoint, outputDir string) *credentialRefresher {
	return newCredentialRefresher(JWTAuthOpts{
		TokenFile:    tokenPath,
		Endpoint:     endpoint,
		CredProvider: "my-provider",
		SandboxId:    "sb-test",
	}, outputDir)
}

// readCredFile reads a credential file through the rotation symlink dir.
func readCredFile(t *testing.T, r *credentialRefresher, key string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Dir(), key))
	require.NoError(t, err)
	return string(data)
}

func TestCredentialRefresher_SuccessfulFetchAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "test-token", "client-123")

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "GetResourceCredential", r.Header.Get("X-Api-Action-Name"))

		var req credentialRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "stsToken", req.CredentialType)
		assert.Equal(t, "client-123", req.ResourceID)
		assert.Equal(t, "my-provider", req.CredentialProviderName)

		resp := credentialResponse{
			RequestID: "resp-1",
			STSToken: &stsToken{
				AccessKeyID:     "AKID-test",
				AccessKeySecret: "AKSECRET-test",
				SecurityToken:   "TOKEN-test",
				Expiration:      time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	outputDir := filepath.Join(tmpDir, "creds")
	refresher := newTestRefresher(tokenPath, srv.URL, outputDir)

	err := refresher.Start(context.Background())
	require.NoError(t, err)
	defer refresher.Stop()

	assert.Equal(t, filepath.Join(outputDir, credentialDataDir), refresher.Dir())

	assert.Equal(t, "AKID-test", readCredFile(t, refresher, mounterutils.KeyAccessKeyId))
	assert.Equal(t, "AKSECRET-test", readCredFile(t, refresher, mounterutils.KeyAccessKeySecret))
	assert.Equal(t, "TOKEN-test", readCredFile(t, refresher, mounterutils.KeySecurityToken))
	assert.NotEmpty(t, readCredFile(t, refresher, mounterutils.KeyExpiration))
}

func TestCredentialRefresher_TokenFileErrors(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name      string
		tokenData string
		wantErr   string
	}{
		{name: "missing file", tokenData: "", wantErr: "read token file"},
		{name: "invalid json", tokenData: "not json", wantErr: "parse token file"},
		{name: "empty accessToken", tokenData: `{"accessToken":"","sandboxClientId":"c1"}`, wantErr: "empty accessToken"},
		{name: "empty sandboxClientId", tokenData: `{"accessToken":"tok","sandboxClientId":""}`, wantErr: "empty sandboxClientId"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenPath := filepath.Join(tmpDir, "token-"+tt.name+".json")
			if tt.tokenData != "" {
				require.NoError(t, os.WriteFile(tokenPath, []byte(tt.tokenData), 0600))
			}
			outputDir := filepath.Join(tmpDir, "creds-"+tt.name)
			refresher := newTestRefresher(tokenPath, "http://localhost:0", outputDir)

			err := refresher.Start(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCredentialRefresher_EndpointErrors(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli")

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	})

	refresher := newTestRefresher(tokenPath, srv.URL, filepath.Join(tmpDir, "creds"))
	err := refresher.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCredentialRefresher_NilSTSToken(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli")

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(credentialResponse{RequestID: "r1", STSToken: nil})
	})

	refresher := newTestRefresher(tokenPath, srv.URL, filepath.Join(tmpDir, "creds"))
	err := refresher.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil stsToken")
}

func TestCredentialRefresher_StopDuringRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli")

	var callCount atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := credentialResponse{
			RequestID: "r1",
			STSToken: &stsToken{
				AccessKeyID:     "ak",
				AccessKeySecret: "sk",
				SecurityToken:   "st",
				Expiration:      time.Now().Add(2 * time.Second).Format(time.RFC3339),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	refresher := newTestRefresher(tokenPath, srv.URL, filepath.Join(tmpDir, "creds"))
	refresher.refreshMargin = 1 * time.Second

	err := refresher.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	refresher.Stop()

	time.Sleep(100 * time.Millisecond)
	finalCount := callCount.Load()
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, finalCount, callCount.Load())
}

func TestCredentialRefresher_CleanupRemovesFiles(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := writeTokenFile(t, tmpDir, "tok", "cli")

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(credentialResponse{
			RequestID: "r1",
			STSToken: &stsToken{
				AccessKeyID: "ak", AccessKeySecret: "sk", SecurityToken: "st",
				Expiration: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		})
	})

	outputDir := filepath.Join(tmpDir, "creds")
	refresher := newTestRefresher(tokenPath, srv.URL, outputDir)
	require.NoError(t, refresher.Start(context.Background()))
	refresher.Stop()

	_, err := os.Stat(refresher.Dir())
	require.NoError(t, err)

	refresher.Cleanup()

	_, err = os.Stat(outputDir)
	assert.True(t, os.IsNotExist(err))
}

func TestCredentialRefresher_CalcSleepDuration(t *testing.T) {
	r := &credentialRefresher{refreshMargin: 5 * time.Minute}

	t.Run("normal expiration", func(t *testing.T) {
		exp := time.Now().Add(30 * time.Minute).Format(time.RFC3339)
		d := r.calcSleepDuration(exp)
		assert.InDelta(t, 25*time.Minute, d, float64(5*time.Second))
	})
	t.Run("near expiration clamps to min", func(t *testing.T) {
		exp := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
		assert.Equal(t, minSleepDuration, r.calcSleepDuration(exp))
	})
	t.Run("past expiration clamps to min", func(t *testing.T) {
		exp := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
		assert.Equal(t, minSleepDuration, r.calcSleepDuration(exp))
	})
	t.Run("invalid format uses margin", func(t *testing.T) {
		assert.Equal(t, 5*time.Minute, r.calcSleepDuration("not-a-date"))
	})
}

func TestJWTAuthOpts_Validate(t *testing.T) {
	valid := JWTAuthOpts{
		SandboxId: "sb", CredProvider: "cp", TokenFile: "/tok", Endpoint: "https://x",
	}
	assert.NoError(t, valid.validate())

	cases := []struct {
		name string
		mut  func(o *JWTAuthOpts)
		want string
	}{
		{"missing sandbox", func(o *JWTAuthOpts) { o.SandboxId = "" }, "sandboxId"},
		{"missing provider", func(o *JWTAuthOpts) { o.CredProvider = "" }, "provider"},
		{"missing token", func(o *JWTAuthOpts) { o.TokenFile = "" }, "token file"},
		{"missing endpoint", func(o *JWTAuthOpts) { o.Endpoint = "" }, "endpoint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := valid
			tc.mut(&o)
			err := o.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// generateTestCA returns a self-signed CA certificate encoded as PEM.
func generateTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestBuildHTTPClient_TLS(t *testing.T) {
	t.Run("no CA uses system root pool", func(t *testing.T) {
		r := newCredentialRefresher(JWTAuthOpts{}, t.TempDir())
		client, err := r.buildHTTPClient()
		require.NoError(t, err)
		tr := client.Transport.(*http.Transport)
		assert.Nil(t, tr.TLSClientConfig.RootCAs, "system root pool expected (RootCAs nil)")
		assert.False(t, tr.TLSClientConfig.InsecureSkipVerify, "TLS verification must never be disabled")
	})

	t.Run("valid CA file is loaded", func(t *testing.T) {
		dir := t.TempDir()
		caPath := filepath.Join(dir, "ca.crt")
		require.NoError(t, os.WriteFile(caPath, generateTestCA(t), 0600))
		r := newCredentialRefresher(JWTAuthOpts{CAFile: caPath}, dir)
		client, err := r.buildHTTPClient()
		require.NoError(t, err)
		tr := client.Transport.(*http.Transport)
		assert.NotNil(t, tr.TLSClientConfig.RootCAs)
		assert.False(t, tr.TLSClientConfig.InsecureSkipVerify)
	})

	t.Run("missing CA file fails, no insecure fallback", func(t *testing.T) {
		r := newCredentialRefresher(JWTAuthOpts{CAFile: "/nonexistent/ca.crt"}, t.TempDir())
		client, err := r.buildHTTPClient()
		require.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "read CA file")
	})

	t.Run("unparsable CA file fails, no insecure fallback", func(t *testing.T) {
		dir := t.TempDir()
		caPath := filepath.Join(dir, "bad.crt")
		require.NoError(t, os.WriteFile(caPath, []byte("not a pem certificate"), 0600))
		r := newCredentialRefresher(JWTAuthOpts{CAFile: caPath}, dir)
		client, err := r.buildHTTPClient()
		require.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "parse CA file")
	})
}
