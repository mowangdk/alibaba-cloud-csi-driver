package interceptors

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"k8s.io/klog/v2"
)

const (
	defaultRefreshMargin   = 5 * time.Minute
	minSleepDuration       = 30 * time.Second
	maxRetryBackoff        = 30 * time.Second
	initialRetryBackoff    = 1 * time.Second
	maxFetchRetries        = 5
	httpTimeout            = 10 * time.Second
	stopWaitTimeout        = 15 * time.Second
	apiActionGetCredential = "GetResourceCredential"
	credentialTypeSTSToken = "stsToken"

	// credentialDataDir is the sub-directory (managed via atomic symlink swap)
	// inside the per-mount output directory that actually holds the STS files.
	// Using a nested "sts" dir mirrors OssfsSecretInterceptor and ensures the
	// symlink path never collides with a pre-existing regular directory.
	credentialDataDir = "sts"
)

// JWTAuthOpts is the resolved configuration for an jwtauth mount.
type JWTAuthOpts struct {
	TokenFile    string
	Endpoint     string
	CredProvider string
	CAFile       string
	SandboxId    string
}

func (o JWTAuthOpts) validate() error {
	if o.SandboxId == "" {
		return fmt.Errorf("sandboxId is required")
	}
	if o.CredProvider == "" {
		return fmt.Errorf("credential provider name is required")
	}
	if o.TokenFile == "" {
		return fmt.Errorf("token file path could not be resolved")
	}
	if o.Endpoint == "" {
		return fmt.Errorf("endpoint could not be resolved")
	}
	return nil
}

type tokenFileContent struct {
	RequestID             string `json:"requestId"`
	AccessToken           string `json:"accessToken"`
	SandboxClientID       string `json:"sandboxClientId"`
	AccessTokenExpiration string `json:"accessTokenExpiration"`
}

type credentialRequest struct {
	CredentialType         string `json:"credentialType"`
	ResourceID             string `json:"resourceId"`
	CredentialProviderName string `json:"credentialProviderName"`
}

type stsToken struct {
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SecurityToken   string `json:"securityToken"`
	Expiration      string `json:"expiration"`
}

type credentialResponse struct {
	RequestID string    `json:"requestId"`
	STSToken  *stsToken `json:"stsToken"`
}

// credentialRefresher fetches scoped STS credentials for an jwtauth
// mount and keeps them fresh in a per-mount directory. Credential files are
// written atomically as a set (via directory symlink swap) so the FUSE client
// never observes a torn AK/SK/token combination during refresh.
type credentialRefresher struct {
	opts          JWTAuthOpts
	outputDir     string // per-mount base dir; entrypoint reads from here
	dataSymlink   string // outputDir/sts symlink swapped atomically on rotation
	refreshMargin time.Duration

	mu     sync.Mutex
	stopCh chan struct{}
	done   chan struct{}
	client *http.Client
}

func newCredentialRefresher(opts JWTAuthOpts, outputDir string) *credentialRefresher {
	return &credentialRefresher{
		opts:          opts,
		outputDir:     outputDir,
		dataSymlink:   filepath.Join(outputDir, credentialDataDir),
		refreshMargin: defaultRefreshMargin,
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Dir returns the directory the FUSE entrypoint should read credential files
// from. It is the symlink that is swapped atomically on each rotation.
func (r *credentialRefresher) Dir() string {
	return r.dataSymlink
}

// Start performs the initial credential fetch synchronously and, on success,
// launches the background refresh loop. The provided context bounds only the
// initial fetch; the loop is stopped via Stop.
func (r *credentialRefresher) Start(ctx context.Context) error {
	if err := os.MkdirAll(r.outputDir, 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	client, err := r.buildHTTPClient()
	if err != nil {
		return fmt.Errorf("build http client: %w", err)
	}
	r.client = client

	cred, err := r.fetchCredentials(ctx)
	if err != nil {
		return fmt.Errorf("initial credential fetch: %w", err)
	}
	if err := r.writeCredentials(cred); err != nil {
		return fmt.Errorf("write initial credentials: %w", err)
	}

	go r.refreshLoop(cred)
	return nil
}

// Stop signals the refresh loop to stop and waits for it to exit, bounded by
// stopWaitTimeout so a hung HTTP call cannot stall driver Terminate.
func (r *credentialRefresher) Stop() {
	r.mu.Lock()
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
	r.mu.Unlock()

	select {
	case <-r.done:
	case <-time.After(stopWaitTimeout):
		klog.Warningf("jwtauth refresher: Stop timed out after %v, goroutine may still be running", stopWaitTimeout)
	}
}

// Cleanup removes the credential files and directories. It should be called
// after Stop once the consuming mount process has exited.
func (r *credentialRefresher) Cleanup() {
	cleanupTokenFiles(r.dataSymlink)
	removeIgnoreNotExist(r.outputDir)
}

// buildHTTPClient delegates to the package-level builder so the refresher and
// other interceptors share identical TLS trust settings.
func (r *credentialRefresher) buildHTTPClient() (*http.Client, error) {
	return buildJWTAuthHTTPClient(r.opts.CAFile)
}

// buildJWTAuthHTTPClient builds the HTTP client used to exchange the
// jwtauth token for STS credentials. This is a security-sensitive
// channel (it carries AK/SK), so TLS verification is never disabled: when a CA
// file is configured it must be readable and parseable, otherwise it fails;
// when no CA file is configured the system root pool is used
// (tls.Config.RootCAs == nil).
func buildJWTAuthHTTPClient(caFile string) (*http.Client, error) {
	tlsConfig := &tls.Config{}
	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %s: %w", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("parse CA file %s: no valid certificate found", caFile)
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{
		Timeout:   httpTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}, nil
}

func readJWTAuthTokenFile(tokenFile string) (*tokenFileContent, error) {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("read token file %s: %w", tokenFile, err)
	}
	var token tokenFileContent
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parse token file: %w", err)
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("token file has empty accessToken")
	}
	if token.SandboxClientID == "" {
		return nil, fmt.Errorf("token file has empty sandboxClientId")
	}
	return &token, nil
}

func (r *credentialRefresher) fetchCredentials(ctx context.Context) (*stsToken, error) {
	return exchangeSTSToken(ctx, r.client, r.opts)
}

// fetchSTSToken performs a one-shot, stateless exchange of the jwtauth
// token for an STS credential. It builds its own HTTP client (honoring the CA
// settings) and never touches the filesystem beyond reading the configured
// token file. Intended for consumers that inject the STS credential directly
// (e.g. alinas mount options) rather than persisting it.
func fetchSTSToken(ctx context.Context, opts JWTAuthOpts) (*stsToken, error) {
	client, err := buildJWTAuthHTTPClient(opts.CAFile)
	if err != nil {
		return nil, fmt.Errorf("build http client: %w", err)
	}
	return exchangeSTSToken(ctx, client, opts)
}

func exchangeSTSToken(ctx context.Context, client *http.Client, opts JWTAuthOpts) (*stsToken, error) {
	token, err := readJWTAuthTokenFile(opts.TokenFile)
	if err != nil {
		return nil, err
	}

	reqBody := credentialRequest{
		CredentialType:         credentialTypeSTSToken,
		ResourceID:             token.SandboxClientID,
		CredentialProviderName: opts.CredProvider,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Action-Name", apiActionGetCredential)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("credential request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("credential endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var credResp credentialResponse
	if err := json.Unmarshal(respBody, &credResp); err != nil {
		return nil, fmt.Errorf("parse credential response: %w", err)
	}
	if credResp.STSToken == nil {
		return nil, fmt.Errorf("credential response has nil stsToken")
	}
	if credResp.STSToken.AccessKeyID == "" || credResp.STSToken.AccessKeySecret == "" {
		return nil, fmt.Errorf("credential response has empty credentials")
	}
	return credResp.STSToken, nil
}

// writeCredentials writes the STS token as an atomic set using the shared
// directory-symlink rotation helper. All four files (AccessKeyId,
// AccessKeySecret, SecurityToken, Expiration) become visible together.
func (r *credentialRefresher) writeCredentials(cred *stsToken) error {
	secrets := map[string]string{
		mounterutils.KeyAccessKeyId:     cred.AccessKeyID,
		mounterutils.KeyAccessKeySecret: cred.AccessKeySecret,
		mounterutils.KeySecurityToken:   cred.SecurityToken,
		mounterutils.KeyExpiration:      cred.Expiration,
	}
	if _, err := rotateTokenFiles(r.dataSymlink, secrets); err != nil {
		return fmt.Errorf("rotate credential files: %w", err)
	}
	return nil
}

func (r *credentialRefresher) refreshLoop(lastCred *stsToken) {
	defer close(r.done)
	for {
		sleepDuration := r.calcSleepDuration(lastCred.Expiration)
		klog.V(4).Infof("jwtauth refresher: next refresh in %v", sleepDuration)

		select {
		case <-r.stopCh:
			return
		case <-time.After(sleepDuration):
		}

		cred, err := r.fetchWithRetry()
		if err != nil {
			klog.Errorf("jwtauth refresher: fetch failed after retries: %v", err)
			continue
		}
		if err := r.writeCredentials(cred); err != nil {
			klog.Errorf("jwtauth refresher: write credentials failed: %v", err)
			continue
		}
		lastCred = cred
		klog.V(2).Infof("jwtauth refresher: credentials refreshed, expires %s", cred.Expiration)
	}
}

func (r *credentialRefresher) fetchWithRetry() (*stsToken, error) {
	backoff := initialRetryBackoff
	var lastErr error
	for i := 0; i < maxFetchRetries; i++ {
		select {
		case <-r.stopCh:
			return nil, fmt.Errorf("stopped")
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		cred, err := r.fetchCredentials(ctx)
		cancel()
		if err == nil {
			return cred, nil
		}
		lastErr = err
		klog.Warningf("jwtauth refresher: fetch attempt %d failed: %v", i+1, err)

		select {
		case <-r.stopCh:
			return nil, fmt.Errorf("stopped")
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxRetryBackoff {
			backoff = maxRetryBackoff
		}
	}
	return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}

func (r *credentialRefresher) calcSleepDuration(expiration string) time.Duration {
	expTime, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		klog.Warningf("jwtauth refresher: parse expiration %q failed, using margin as sleep: %v", expiration, err)
		return r.refreshMargin
	}
	until := time.Until(expTime) - r.refreshMargin
	if until < minSleepDuration {
		return minSleepDuration
	}
	return until
}
