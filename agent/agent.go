// Package agent embeds the shell scripts that run on the Caddy server.
package agent

import (
	_ "embed"
	"strings"
)

//go:embed caddyweb-agent
var Script string

//go:embed install-agent.sh
var installer string

// Installer returns install-agent.sh with the agent script and CaddyWeb's
// public key filled in.
func Installer(publicKey string) string {
	s := strings.Replace(installer, "__PUBLIC_KEY__", strings.TrimSpace(publicKey), 1)
	return strings.Replace(s, "__AGENT_SCRIPT__", strings.TrimRight(Script, "\n"), 1)
}
