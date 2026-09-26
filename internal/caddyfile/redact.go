package caddyfile

import (
	"strings"
)

// Mask replaces secret values shown to non-admin users.
const Mask = "••••••••"

var secretSubdirectives = []string{"token", "secret", "password", "passwd", "credential"}

func isSecretName(name string) bool {
	name = strings.ToLower(name)
	if name == "key" || strings.HasSuffix(name, "_key") {
		return true
	}
	for _, s := range secretSubdirectives {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

func isPlaceholder(tok string) bool {
	t := Unquote(tok)
	return strings.HasPrefix(t, "{env.") || strings.HasPrefix(t, "{$")
}

func maskTok(tok string) string {
	if isPlaceholder(tok) {
		return tok
	}
	return Mask
}

// RedactNodes returns a copy of nodes with credentials masked: DNS provider
// settings, basic_auth hashes and any sub-option whose name looks secret.
func RedactNodes(nodes []*Node) []*Node {
	out := CloneNodes(nodes)
	redact(out, false)
	return out
}

func redact(nodes []*Node, inSecretBlock bool) {
	for _, n := range nodes {
		if n.Type != "directive" || len(n.Tokens) == 0 {
			continue
		}
		name := n.Tokens[0]
		secretBlock := inSecretBlock
		switch name {
		case "acme_dns", "dns":
			// acme_dns <provider> [args...] { ... }
			for i := 2; i < len(n.Tokens); i++ {
				n.Tokens[i] = maskTok(n.Tokens[i])
			}
			secretBlock = true
		case "acme_eab", "basic_auth", "basicauth":
			secretBlock = true
			if name == "acme_eab" {
				for i := 1; i < len(n.Tokens); i++ {
					n.Tokens[i] = maskTok(n.Tokens[i])
				}
			}
		default:
			if inSecretBlock || isSecretName(name) {
				for i := 1; i < len(n.Tokens); i++ {
					n.Tokens[i] = maskTok(n.Tokens[i])
				}
			}
		}
		if n.Block != nil {
			redact(n.Block, secretBlock)
		}
	}
}

// RedactSegment returns a redacted copy of a segment.
func RedactSegment(s *Segment) *Segment {
	c := s.Clone()
	c.Nodes = RedactNodes(s.Nodes)
	c.MarkDirty()
	return c
}

// RedactText re-renders a whole Caddyfile with secrets masked. Unchanged
// blocks keep their original layout; only blocks containing secrets are
// re-formatted.
func RedactText(src string) string {
	doc, err := Parse(src)
	if err != nil {
		return "# (file could not be parsed for display)\n"
	}
	for _, p := range doc.Parts {
		if p.Seg == nil {
			continue
		}
		before := FormatNodes(p.Seg.Nodes, "\t", 1)
		red := RedactNodes(p.Seg.Nodes)
		if FormatNodes(red, "\t", 1) != before {
			p.Seg.Nodes = red
			p.Seg.MarkDirty()
		}
	}
	return doc.String()
}
