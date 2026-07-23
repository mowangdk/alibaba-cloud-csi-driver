package server

import (
	"fmt"
	"os"
)

// AgentIdentityCAFilePath is the default path for the agent identity CA certificate.
// See AgentIdentityConfig for file placement scenarios.
const AgentIdentityCAFilePath = "/etc/ssl/certs/agent-identity/ca.crt"

const defaultJWTAuthEndpoint = "https://credential-provider.ack-agent-identity.svc:8443/"

// GetAgentIdentityCAFilePath resolves the CA file path for agent identity authentication.
// It prefers the SSL_CERT_FILE environment variable; if unset or empty, it falls back to
// AgentIdentityCAFilePath.
func GetAgentIdentityCAFilePath() string {
	if p := os.Getenv("SSL_CERT_FILE"); p != "" {
		return p
	}
	return AgentIdentityCAFilePath
}

func GetJWTAuthEndpoint() string {
	if ep := os.Getenv("JWTAUTH_ENDPOINT"); ep != "" {
		return ep
	}
	return defaultJWTAuthEndpoint
}

func GetJWTAuthTokenFilePath(sandboxId string) string {
	return fmt.Sprintf("/var/opt/sandbox/agent-token/%s.token", sandboxId)
}
