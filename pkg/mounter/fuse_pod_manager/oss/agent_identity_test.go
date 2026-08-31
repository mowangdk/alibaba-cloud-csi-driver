package oss

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateAgentIdentity(t *testing.T) {
	tests := []struct {
		name     string
		opts     *Options
		setupEnv func(t *testing.T)
		wantErr  bool
		errMsg   string
	}{
		{
			name: "missing sandboxId",
			opts: &Options{
				SandboxCredProviderName: "aliyun-one",
			},
			wantErr: true,
			errMsg:  "missing sandboxId in volume attributes",
		},
		{
			name: "missing sandboxCredProviderName",
			opts: &Options{
				SandboxId: "sandbox-123",
			},
			wantErr: true,
			errMsg:  "missing sandboxCredProviderName in volume attributes",
		},
		{
			name: "AGENT_IDENTITY_ENDPOINT not set",
			opts: &Options{
				SandboxId:               "sandbox-123",
				SandboxCredProviderName: "aliyun-one",
			},
			wantErr: true,
			errMsg:  "AGENT_IDENTITY_ENDPOINT is not set",
		},
		{
			name: "AGENT_IDENTITY_TOKEN_DIR not set",
			opts: &Options{
				SandboxId:               "sandbox-123",
				SandboxCredProviderName: "aliyun-one",
			},
			setupEnv: func(t *testing.T) {
				t.Setenv("AGENT_IDENTITY_ENDPOINT", "https://endpoint.example.com")
			},
			wantErr: true,
			errMsg:  "AGENT_IDENTITY_TOKEN_DIR is not set",
		},
		{
			name: "success",
			opts: &Options{
				SandboxId:               "sandbox-123",
				SandboxCredProviderName: "aliyun-one",
			},
			setupEnv: func(t *testing.T) {
				t.Setenv("AGENT_IDENTITY_ENDPOINT", "https://endpoint.example.com")
				t.Setenv("AGENT_IDENTITY_TOKEN_DIR", "/var/opt/sandbox/agent-token")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv != nil {
				tt.setupEnv(t)
			}
			err := ValidateAgentIdentity(tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.EqualError(t, err, tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMakeAgentIdentityConfig(t *testing.T) {
	opts := &Options{
		SandboxId:               "sandbox-123",
		SandboxCredProviderName: "aliyun-one",
	}

	cfg := MakeAgentIdentityConfig(opts)

	assert.NotNil(t, cfg)
	assert.Equal(t, "aliyun-one", cfg.CredProviderName)
	assert.Equal(t, "sandbox-123", cfg.SandboxId)
}

func TestGetAgentIdentityOptions(t *testing.T) {
	t.Setenv("AGENT_IDENTITY_ENDPOINT", "https://endpoint.example.com")
	t.Setenv("AGENT_IDENTITY_TOKEN_DIR", "/var/opt/sandbox/agent-token")

	opts := &Options{
		SandboxId:               "sandbox-123",
		SandboxCredProviderName: "aliyun-one",
	}

	mountOpts := GetAgentIdentityOptions(opts)

	expected := []string{
		"agent_identity_endpoint=https://endpoint.example.com",
		"agent_identity_token_file=/var/opt/sandbox/agent-token/sandbox-123.token",
		"agent_identity_cred_provider=aliyun-one",
	}

	assert.Equal(t, expected, mountOpts)
}

func TestGetAgentIdentityOptions_DifferentSandboxId(t *testing.T) {
	t.Setenv("AGENT_IDENTITY_ENDPOINT", "https://custom-endpoint:8443")
	t.Setenv("AGENT_IDENTITY_TOKEN_DIR", "/custom/token/dir")

	opts := &Options{
		SandboxId:               "sandbox-abc-456",
		SandboxCredProviderName: "oss-readonly",
	}

	mountOpts := GetAgentIdentityOptions(opts)

	assert.Len(t, mountOpts, 3)
	assert.Contains(t, mountOpts[0], "agent_identity_endpoint=https://custom-endpoint:8443")
	assert.Contains(t, mountOpts[1], "agent_identity_token_file=/custom/token/dir/sandbox-abc-456.token")
	assert.Contains(t, mountOpts[2], "agent_identity_cred_provider=oss-readonly")
}
