package caddyfile

import (
	"fmt"
	"strings"
)

// ResolveRaw replaces every node that carries Raw text (sent by the UI's
// raw editors) with the nodes parsed from that text, recursively.
func ResolveRaw(nodes []*Node) ([]*Node, error) {
	if nodes == nil {
		return nil, nil
	}
	out := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.Raw != nil {
			parsed, err := ParseNodes(*n.Raw)
			if err != nil {
				return nil, fmt.Errorf("in raw text %q: %w", firstLine(*n.Raw), err)
			}
			if len(parsed) > 0 {
				parsed[0].Blank = n.Blank
			}
			out = append(out, parsed...)
			continue
		}
		if n.Type == "" {
			n.Type = "directive"
		}
		if n.Type == "comment" {
			t := strings.TrimSpace(n.Text)
			if t == "" {
				continue
			}
			if !strings.HasPrefix(t, "#") {
				t = "# " + t
			}
			n.Text = t
			n.Tokens = nil
			n.Block = nil
			out = append(out, n)
			continue
		}
		if len(n.Tokens) == 0 {
			continue // empty rows from the UI
		}
		for i, tok := range n.Tokens {
			if err := checkToken(tok); err != nil {
				return nil, fmt.Errorf("directive %q: %w", strings.Join(n.Tokens, " "), err)
			}
			n.Tokens[i] = tok
		}
		if n.Comment != "" {
			c := strings.TrimSpace(strings.ReplaceAll(n.Comment, "\n", " "))
			if !strings.HasPrefix(c, "#") {
				c = "# " + c
			}
			n.Comment = c
		}
		if n.Block != nil {
			b, err := ResolveRaw(n.Block)
			if err != nil {
				return nil, err
			}
			n.Block = b
		}
		out = append(out, n)
	}
	return out, nil
}

// checkToken makes sure a single token from the UI can't smuggle in extra
// structure (a newline or an unbalanced brace would change the meaning of
// the surrounding file).
func checkToken(tok string) error {
	if tok == "" {
		return fmt.Errorf("empty value")
	}
	toks, err := Lex(tok)
	if err != nil {
		return err
	}
	if len(toks) != 1 || toks[0].Kind != TokWord {
		return fmt.Errorf("value %q must be a single word; wrap it in double quotes if it contains spaces", tok)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + "…"
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

// CheckSegment validates a segment built by the UI before it is written.
func CheckSegment(s *Segment) error {
	for i, c := range s.Comments {
		c = strings.TrimSpace(strings.ReplaceAll(c, "\n", " "))
		if c == "" {
			continue
		}
		if !strings.HasPrefix(c, "#") {
			c = "# " + c
		}
		s.Comments[i] = c
	}
	// drop empty comment lines
	cs := s.Comments[:0]
	for _, c := range s.Comments {
		if strings.TrimSpace(c) != "" {
			cs = append(cs, c)
		}
	}
	s.Comments = cs
	if s.HeaderComment != "" && !strings.HasPrefix(strings.TrimSpace(s.HeaderComment), "#") {
		s.HeaderComment = "# " + strings.TrimSpace(s.HeaderComment)
	}
	switch s.Kind {
	case KindGlobal:
		s.Header = nil
	case KindSite, KindSnippet, KindNamedRoute, KindDirective:
		if len(s.Header) == 0 {
			return fmt.Errorf("a site needs at least one address")
		}
		for _, h := range s.Header {
			if err := checkToken(h); err != nil {
				return fmt.Errorf("address: %w", err)
			}
			if h == "{" || h == "}" {
				return fmt.Errorf("address may not be a brace")
			}
		}
	default:
		return fmt.Errorf("unknown block kind %q", s.Kind)
	}
	nodes, err := ResolveRaw(s.Nodes)
	if err != nil {
		return err
	}
	s.Nodes = nodes
	s.Braces = true
	return nil
}

// Walk visits every node depth-first; parents lists the enclosing directives.
func Walk(nodes []*Node, fn func(n *Node, parents []*Node)) {
	var walk func([]*Node, []*Node)
	walk = func(ns []*Node, parents []*Node) {
		for _, n := range ns {
			fn(n, parents)
			if n.Block != nil {
				walk(n.Block, append(parents, n))
			}
		}
	}
	walk(nodes, nil)
}

// Unquote returns the value of a raw token without surrounding quotes.
func Unquote(tok string) string {
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return strings.ReplaceAll(tok[1:len(tok)-1], `\"`, `"`)
	}
	if len(tok) >= 2 && tok[0] == '`' && tok[len(tok)-1] == '`' {
		return tok[1 : len(tok)-1]
	}
	return tok
}
