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
			if err := checkComment(t); err != nil {
				return nil, err
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
			c := strings.TrimSpace(n.Comment)
			if !strings.HasPrefix(c, "#") {
				c = "# " + c
			}
			if err := checkComment(c); err != nil {
				return nil, err
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
	if strings.ContainsAny(tok, "\r\n") && !strings.HasPrefix(tok, "<<") {
		return fmt.Errorf("value %q may not contain line breaks", firstLine(tok))
	}
	if strings.HasPrefix(tok, "<<") && !strings.Contains(tok, "\n") {
		return fmt.Errorf("value %q can't start with <<", tok)
	}
	if strings.HasSuffix(tok, "\\") {
		return fmt.Errorf("value %q can't end with a backslash", tok)
	}
	toks, err := Lex(tok)
	if err != nil {
		return err
	}
	if len(toks) != 1 || toks[0].Kind != TokWord || toks[0].Text != tok {
		return fmt.Errorf("value %q must be a single word; wrap it in double quotes if it contains spaces", tok)
	}
	return nil
}

// checkComment makes sure a comment from the UI is exactly one line.
func checkComment(c string) error {
	if strings.ContainsAny(c, "\r\n") {
		return fmt.Errorf("comments must be a single line")
	}
	toks, err := Lex(c)
	if err != nil || len(toks) != 1 || toks[0].Kind != TokComment {
		return fmt.Errorf("invalid comment %q", c)
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
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.HasPrefix(c, "#") {
			c = "# " + c
		}
		if err := checkComment(c); err != nil {
			return err
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
	if hc := strings.TrimSpace(s.HeaderComment); hc != "" {
		if !strings.HasPrefix(hc, "#") {
			hc = "# " + hc
		}
		if err := checkComment(hc); err != nil {
			return err
		}
		s.HeaderComment = hc
	} else {
		s.HeaderComment = ""
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
		// The kind must match what the header means when the file is read back.
		h0 := s.Header[0]
		isSnippet := len(s.Header) == 1 && strings.HasPrefix(h0, "(") && strings.HasSuffix(h0, ")")
		isRoute := strings.HasPrefix(h0, "&(")
		switch {
		case s.Kind == KindSite && (isSnippet || isRoute || h0 == "import"):
			return fmt.Errorf("%q is not a site address", h0)
		case s.Kind == KindSnippet && !isSnippet:
			return fmt.Errorf("a snippet name must look like (name)")
		case s.Kind == KindNamedRoute && !isRoute:
			return fmt.Errorf("a named route must look like &(name)")
		case s.Kind == KindDirective && h0 != "import":
			return fmt.Errorf("only top-level import lines are supported")
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
