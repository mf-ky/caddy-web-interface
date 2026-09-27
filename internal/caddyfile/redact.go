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

// sensitive header names whose values are hidden from non-admins
var secretHeaders = []string{"authorization", "token", "secret", "api-key", "apikey", "password", "cookie", "x-auth"}

func isSecretHeader(field string) bool {
	f := strings.ToLower(strings.TrimLeft(Unquote(field), "+-?>"))
	for _, s := range secretHeaders {
		if strings.Contains(f, s) {
			return true
		}
	}
	return false
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
		name := Unquote(n.Tokens[0])
		secretBlock := inSecretBlock
		switch name {
		case "acme_dns", "dns", "dynamic_dns", "provider", "issuer", "cert_issuer", "acme_ca_root":
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
		case "header_up", "header_down", "header", "request_header":
			// header [matcher] Field value… — hide values of sensitive headers
			for i := 1; i < len(n.Tokens)-1; i++ {
				if isSecretHeader(n.Tokens[i]) {
					for j := i + 1; j < len(n.Tokens); j++ {
						n.Tokens[j] = maskTok(n.Tokens[j])
					}
					break
				}
			}
			if name == "header" && n.Block != nil {
				for _, c := range n.Block {
					if c.Type == "directive" && len(c.Tokens) > 1 && isSecretHeader(c.Tokens[0]) {
						for j := 1; j < len(c.Tokens); j++ {
							c.Tokens[j] = maskTok(c.Tokens[j])
						}
					}
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

// SecretValues lists every value RedactText would hide in src, so error
// messages shown to non-admins can be scrubbed as well.
func SecretValues(src string) []string {
	doc, err := Parse(src)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, sg := range doc.Segments() {
		red := RedactNodes(sg.Nodes)
		var walk func(a, b []*Node)
		walk = func(a, b []*Node) {
			for i := range a {
				if i >= len(b) {
					return
				}
				for j := range a[i].Tokens {
					if j < len(b[i].Tokens) && a[i].Tokens[j] != b[i].Tokens[j] {
						for _, v := range []string{a[i].Tokens[j], Unquote(a[i].Tokens[j])} {
							if len(v) >= 4 && !seen[v] {
								seen[v] = true
								out = append(out, v)
							}
						}
					}
				}
				walk(a[i].Block, b[i].Block)
			}
		}
		walk(sg.Nodes, red)
	}
	return out
}

// Scrub replaces every secret value in s with Mask.
func Scrub(s string, secrets []string) string {
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, Mask)
	}
	return s
}
