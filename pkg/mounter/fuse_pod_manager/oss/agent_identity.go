package oss

import (
	"fmt"

	fpm "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/fuse_pod_manager"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils/agentidentity"
)

// ValidateAgentIdentity validates the agent identity auth configuration.
// Returns error if required fields are missing or environment variables are not set.
func ValidateAgentIdentity(o *Options) error {
	if o.SandboxId == "" {
		return fmt.Errorf("missing sandboxId in volume attributes")
	}
	if o.SandboxCredProviderName == "" {
		return fmt.Errorf("missing sandboxCredProviderName in volume attributes")
	}
	if agentidentity.GetEndpoint() == "" {
		return fmt.Errorf("AGENT_IDENTITY_ENDPOINT is not set")
	}
	if agentidentity.GetTokenDir() == "" {
		return fmt.Errorf("AGENT_IDENTITY_TOKEN_DIR is not set")
	}
	return nil
}

// MakeAgentIdentityConfig creates an AgentIdentityConfig from the options.
func MakeAgentIdentityConfig(o *Options) *fpm.AgentIdentityConfig {
	return &fpm.AgentIdentityConfig{
		CredProviderName: o.SandboxCredProviderName,
		SandboxId:        o.SandboxId,
	}
}

// GetAgentIdentityOptions returns the mount options for agent identity auth.
func GetAgentIdentityOptions(o *Options) []string {
	// Env vars are validated in ValidateAgentIdentity.
	return []string{
		fmt.Sprintf("agent_identity_endpoint=%s", agentidentity.GetEndpoint()),
		fmt.Sprintf("agent_identity_token_file=%s", agentidentity.GetTokenFilePath(o.SandboxId)),
		fmt.Sprintf("agent_identity_cred_provider=%s", o.SandboxCredProviderName),
		// agent_identity_ca_file is not added here — it is optional and only appended
		// by ApplyOptionDefaults if the file is readable. See AgentIdentityConfig for details.
	}
}
