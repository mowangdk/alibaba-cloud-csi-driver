/*
Copyright 2020 The Alibaba Cloud Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	cre "github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials/provider"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils/crypto"
	"k8s.io/klog/v2"
)

const (
	// ConfigPath the secret mount file
	ConfigPath = "/var/addon/token-config"

	addonTokenExpirationScale = 0.9
)

// ManageTokens 定义资源账号
type ManageTokens struct {
	// AccessKeyId key
	AccessKeyID string
	// AccessKeySecret key
	AccessKeySecret string
	// SecurityToken key
	SecurityToken string

	// expire time
	ExpireAt time.Time
}

// AccessControlMode is int, represents different modes
type AccessControlMode int

// AccessControlMode includes AccessKey, ManagedToken, EcsRamRole, Credential, RoleArnToken, five types of access control
const (
	AccessKey AccessControlMode = iota
	ManagedToken
	Credential
)

// AccessControl is access control option
type AccessControl struct {
	AccessKeyID     string
	AccessKeySecret string
	StsToken        string
	RoleArn         string
	Config          *sdk.Config
	Credential      auth.Credential
	UseMode         AccessControlMode
}

// isUnderProc checks whether the literal cleaned path is under /proc.
func isUnderProc(cleaned string) bool {
	return cleaned == "/proc" || strings.HasPrefix(cleaned, "/proc/")
}

// isUnderPATH checks whether the resolved path falls under any PATH directory.
func isUnderPATH(resolved string) (string, bool) {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return "", false
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		dir = filepath.Clean(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if resolved == dir || strings.HasPrefix(resolved, dir+string(os.PathSeparator)) {
			return dir, true
		}
	}
	return "", false
}

// resolveSymlinks resolves symlinks for cleaned, tolerating non-existent
// trailing components. It first tries to resolve the full path. Only when
// that fails because the path does not exist yet does it recursively resolve
// the parent directory, then re-append the missing trailing component, so the
// result is the resolved deepest existing ancestor joined with the full
// non-existent suffix. Symlinks in any existing ancestor directory are still
// followed and cannot be used to bypass the sensitive-path checks. Any
// resolution error other than non-existence is returned as-is, and an error
// is also returned if no ancestor up to the root can be resolved.
func resolveSymlinks(cleaned string) (string, error) {
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		// Reached the root without resolving any ancestor.
		return "", fmt.Errorf("cannot resolve any ancestor of %s: %w", cleaned, err)
	}
	resolvedParent, err := resolveSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}

// ValidatePath checks that path is a safe mount path.
//
// It requires the path to be absolute, then:
//  1. Rejects paths literally under /proc (even if symlinks resolve elsewhere).
//  2. Resolves symlinks and rejects paths whose real location is under a PATH directory.
//
// Note: the kubelet root dir containment check is done by the caller.
func ValidatePath(path string) (bool, error) {
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("path %s must be an absolute path", path)
	}

	cleaned := filepath.Clean(path)
	if isUnderProc(cleaned) {
		return false, fmt.Errorf("path %s is under sensitive path /proc", path)
	}

	// Resolve symlinks. When trailing components do not exist yet, the
	// deepest existing ancestor directory is resolved and the non-existent
	// suffix is re-appended, so symlinks in ancestor directories are still
	// followed and cannot be used to bypass the sensitive-path checks below.
	resolved, err := resolveSymlinks(cleaned)
	if err != nil {
		return false, fmt.Errorf("failed to resolve symlinks for path %s: %w", path, err)
	}

	// Also reject symlinks that resolve into /proc.
	if isUnderProc(resolved) {
		return false, fmt.Errorf("path %s is under sensitive path /proc", path)
	}

	if dir, ok := isUnderPATH(resolved); ok {
		return false, fmt.Errorf("path %s is under PATH directory %s", path, dir)
	}

	return true, nil
}

func getManagedAddonToken() AccessControl {
	tokens := getManagedToken()
	scheme := "https"
	if e := os.Getenv("ALICLOUD_CLIENT_SCHEME"); e != "" {
		scheme = e
	}
	config := sdk.NewConfig().WithScheme(scheme)
	credent := &cre.StsTokenCredential{
		AccessKeyId:       tokens.AccessKeyID,
		AccessKeySecret:   tokens.AccessKeySecret,
		AccessKeyStsToken: tokens.SecurityToken,
	}

	return AccessControl{AccessKeyID: tokens.AccessKeyID, AccessKeySecret: tokens.AccessKeySecret, StsToken: tokens.SecurityToken, UseMode: ManagedToken, Config: config, Credential: credent}
}

// GetAccessControl  1、Read default ak from local file. 2、If local default ak is not exist, then read from STS.
func GetAccessControl() AccessControl {
	if os.Getenv("USE_OIDC_AUTH_INNER") == "true" {
		klog.Fatal("USE_OIDC_AUTH_INNER is no longer supported")
	}

	//1、Get AK from Env
	acLocalAK := GetEnvAK()
	if len(acLocalAK.AccessKeyID) != 0 && len(acLocalAK.AccessKeySecret) != 0 {
		klog.Info("Get AK: use ENV AK")
		return acLocalAK
	}

	//2、Get AK from Credential Files
	acCredentialAK := getCredentialAK()
	if acCredentialAK.Config != nil && acCredentialAK.Credential != nil {
		klog.Info("Get AK: use Credential AK")
		return acCredentialAK
	}

	//3、Get AK from ManagedToken
	acAddonToken := getManagedAddonToken()
	if len(acAddonToken.AccessKeyID) != 0 {
		klog.Info("Get AK: use Managed Addon Token")
		return acAddonToken
	}

	//4、Get AK from ECS StsToken
	acStsToken := getStsToken()
	klog.Info("Get AK: use ECS RamRole Token")
	return acStsToken
}

// GetEnvAK read ak from local ENV
func GetEnvAK() AccessControl {
	accessKeyID, accessSecret := "", ""
	accessKeyID = os.Getenv("ACCESS_KEY_ID")
	accessSecret = os.Getenv("ACCESS_KEY_SECRET")

	scheme := "https"
	if e := os.Getenv("ALICLOUD_CLIENT_SCHEME"); e != "" {
		scheme = e
	}
	config := sdk.NewConfig().WithScheme(scheme)

	credent := &cre.AccessKeyCredential{
		AccessKeyId:     strings.TrimSpace(accessKeyID),
		AccessKeySecret: strings.TrimSpace(accessSecret),
	}
	return AccessControl{AccessKeyID: strings.TrimSpace(accessKeyID), AccessKeySecret: strings.TrimSpace(accessSecret), UseMode: AccessKey, Config: config, Credential: credent}
}

// GetStsToken get STS token and token from ecs meta server
func getStsToken() AccessControl {
	scheme := "https"
	if e := os.Getenv("ALICLOUD_CLIENT_SCHEME"); e != "" {
		scheme = e
	}
	config := sdk.NewConfig().WithScheme(scheme)
	return AccessControl{UseMode: Credential, Config: config, Credential: cre.NewEcsRamRoleCredential("")}
}

// GetManagedToken get ak from csi secret
func getManagedToken() (tokens ManageTokens) {
	if f, err := os.Open(ConfigPath); err == nil {
		defer f.Close()
		newToken, err := crypto.RamTokenParse(f)
		if err != nil {
			klog.Errorf("failed to decrypt new token: %v", err)
			return ManageTokens{}
		}
		expireAt, err := time.Parse(time.RFC3339, newToken.Expiration)
		if err != nil {
			klog.Errorf("failed to parse expiration: %q: %v", newToken.Expiration, err)
			return ManageTokens{}
		}
		tokens.AccessKeyID = newToken.AccessKeyId
		tokens.AccessKeySecret = newToken.AccessKeySecret
		tokens.SecurityToken = newToken.SecurityToken
		tokens.ExpireAt = expireAt
	}
	return tokens
}

// getCredentialAK get credential and config from credential files.
func getCredentialAK() AccessControl {
	envProvider := provider.NewEnvProvider()
	profileProvider := provider.NewProfileProvider()
	pc := provider.NewProviderChain([]provider.Provider{envProvider, profileProvider})
	credential, err := pc.Resolve()
	if err != nil {
		if !strings.Contains(err.Error(), "no credential found") {
			klog.Errorf("Failed to resolve an authentication provider: %v", err)
		}
	}
	scheme := "https"
	if e := os.Getenv("ALICLOUD_CLIENT_SCHEME"); e != "" {
		scheme = e
	}
	config := sdk.NewConfig().WithScheme(scheme)
	return AccessControl{Config: config, Credential: credential, UseMode: Credential}
}
