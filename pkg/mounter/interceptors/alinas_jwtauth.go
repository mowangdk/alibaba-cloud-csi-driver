package interceptors

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"k8s.io/klog/v2"
)

var _ mounter.MountInterceptor = AlinasJWTAuthInterceptor

const (
	// alinas mount options that carry the STS credential. The alinas client
	// accepts the STS triple directly as mount options:
	//   mount -t alinas -o tls,access_key_id=<AK>,access_key_secret=<SK>,security_token=<TOKEN> ...
	optAlinasAccessKeyID     = "access_key_id"
	optAlinasAccessKeySecret = "access_key_secret"
	optAlinasSecurityToken   = "security_token"
	optAlinasTLS             = "tls"
)

// AlinasJWTAuthInterceptor provisions scoped STS credentials for alinas
// mounts that use authType=jwtauth.
//
// Unlike JWTAuthInterceptor (customfuse), the alinas client consumes the
// STS credential directly as mount options and refreshes it out-of-band, so
// this interceptor never touches the filesystem: it exchanges the sandbox
// jwtauth token for an STS token entirely in memory and injects the
// resolved credential into op.Options. Nothing is written to disk.
//
// For any other authType (including the empty default) it is a no-op.
func AlinasJWTAuthInterceptor(ctx context.Context, op *mounter.MountOperation, handler mounter.MountHandler) error {
	if op == nil {
		return handler(ctx, op)
	}
	// alinas mount options may arrive as comma-joined compound strings
	// (e.g. "tls,vers=3,authType=jwtauth"), so flatten before indexing.
	flat := flattenMountOptions(op.Options)
	idx := mounterutils.IndexMountOptions(flat)
	if idx[optAuthType] != AuthTypeJWTAuth {
		return handler(ctx, op)
	}

	opts := resolveJWTAuthOpts(idx)
	if err := opts.validate(); err != nil {
		return fmt.Errorf("jwtauth config error: %w", err)
	}

	cred, err := fetchSTSToken(ctx, opts)
	if err != nil {
		// STS acquisition failure is fatal: alinas requires cloud credentials
		// and silently falling back to static AK/SK would broaden the effective
		// permission scope. Fail the mount instead.
		return fmt.Errorf("jwtauth fetch STS token: %w", err)
	}

	op.Options = injectAlinasSTSOptions(flat, cred)
	klog.V(4).InfoS("injected jwtauth STS credential into alinas mount options", "target", op.Target)
	return handler(ctx, op)
}

// flattenMountOptions splits any comma-joined compound options into individual
// options so key lookups and rewriting operate on single "key=value" entries.
func flattenMountOptions(options []string) []string {
	flat := make([]string, 0, len(options))
	for _, o := range options {
		for _, part := range mounterutils.SplitMountOptions(o) {
			if part = strings.TrimSpace(part); part != "" {
				flat = append(flat, part)
			}
		}
	}
	return flat
}

// injectAlinasSTSOptions strips the infrastructure-only jwtauth options
// and any pre-existing static credential options, then appends the resolved STS
// triple plus tls. Input must already be flattened. The returned slice is safe
// to pass to the alinas mount.
func injectAlinasSTSOptions(flatOptions []string, cred *stsToken) []string {
	result := make([]string, 0, len(flatOptions)+4)
	hasTLS := false
	for _, opt := range flatOptions {
		key, _, _ := strings.Cut(opt, "=")
		switch key {
		case optSandboxId, optSandboxCredProviderName,
			optJWTAuthEndpoint, optJWTAuthTokenFile,
			optJWTAuthCredProvider, optJWTAuthCAFile,
			optAlinasAccessKeyID, optAlinasAccessKeySecret, optAlinasSecurityToken:
			continue
		case optAlinasTLS:
			hasTLS = true
		}
		result = append(result, opt)
	}
	if !hasTLS {
		result = append(result, optAlinasTLS)
	}
	result = append(result,
		optAlinasAccessKeyID+"="+cred.AccessKeyID,
		optAlinasAccessKeySecret+"="+cred.AccessKeySecret,
		optAlinasSecurityToken+"="+cred.SecurityToken,
	)
	return result
}
