package caddyfile

import (
	"strings"
)

type parser struct {
	src  string
	toks []Token
	p    int
}

// Parse reads a whole Caddyfile.
func Parse(src string) (*Document, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	ps := &parser{src: src, toks: toks}
	doc := &Document{Indent: DetectIndent(src), CRLF: strings.Count(src, "\r\n") > strings.Count(src, "\n")/2}

	fillerStart := 0
	var pending []Token // comment lines directly above the next segment
	prevNewline := true
	newlineRun := 0

	for ps.p < len(toks) {
		t := toks[ps.p]
		switch t.Kind {
		case TokNewline:
			newlineRun++
			if newlineRun >= 2 {
				pending = nil // blank line: comments above are detached
			}
			prevNewline = true
			ps.p++
			continue
		case TokComment:
			if !prevNewline && len(pending) == 0 {
				// trailing comment on a line we don't own; leave in filler
				ps.p++
				continue
			}
			pending = append(pending, t)
			newlineRun = 0
			prevNewline = false
			ps.p++
			continue
		case TokClose:
			return nil, &SyntaxError{Line: t.Line, Msg: "unexpected '}' at top level"}
		}

		// Start of a segment.
		segStart := lineStart(src, t.Start)
		if len(pending) > 0 {
			segStart = lineStart(src, pending[0].Start)
		}
		seg, segEnd, err := ps.parseSegment(len(doc.Segments()) == 0)
		if err != nil {
			return nil, err
		}
		for _, c := range pending {
			seg.Comments = append(seg.Comments, c.Text)
		}
		pending = nil
		if segStart > fillerStart {
			doc.Parts = append(doc.Parts, &Part{Filler: src[fillerStart:segStart]})
		}
		seg.raw = src[segStart:segEnd]
		doc.Parts = append(doc.Parts, &Part{Seg: seg})
		fillerStart = segEnd
		for ps.p < len(toks) && toks[ps.p].Start < segEnd {
			ps.p++
		}
		prevNewline = true
		newlineRun = 1
	}
	if fillerStart < len(src) {
		doc.Parts = append(doc.Parts, &Part{Filler: src[fillerStart:]})
	}
	return doc, nil
}

func lineStart(src string, off int) int {
	i := strings.LastIndexByte(src[:off], '\n')
	return i + 1
}

// lineEnd returns the offset just past the newline that ends the line
// containing off (or len(src)).
func lineEnd(src string, off int) int {
	i := strings.IndexByte(src[off:], '\n')
	if i < 0 {
		return len(src)
	}
	return off + i + 1
}

func (ps *parser) parseSegment(first bool) (*Segment, int, error) {
	t := ps.toks[ps.p]
	seg := &Segment{Braces: true}

	if t.Kind == TokOpen {
		if !first {
			return nil, 0, &SyntaxError{Line: t.Line, Msg: "a block with no site address is only allowed first in the file (global options)"}
		}
		seg.Kind = KindGlobal
		nodes, next, openComment, err := ps.parseBlock(ps.p+1, t.Line, true)
		if err != nil {
			return nil, 0, err
		}
		seg.Nodes, seg.HeaderComment = nodes, openComment
		ps.p = next
		return seg, lineEnd(ps.src, ps.toks[next-1].End), nil
	}

	// Collect header tokens; a trailing comma continues onto the next line.
	var header []string
	for ps.p < len(ps.toks) {
		t = ps.toks[ps.p]
		if t.Kind == TokWord {
			header = append(header, t.Text)
			ps.p++
			continue
		}
		if t.Kind == TokNewline && len(header) > 0 && strings.HasSuffix(header[len(header)-1], ",") {
			ps.p++
			continue
		}
		break
	}
	seg.Header = header

	switch {
	case len(header) == 1 && strings.HasPrefix(header[0], "(") && strings.HasSuffix(header[0], ")"):
		seg.Kind = KindSnippet
	case strings.HasPrefix(header[0], "&("):
		seg.Kind = KindNamedRoute
	default:
		seg.Kind = KindSite
	}

	// Find the opening brace: same line, or alone on the following line.
	open := -1
	q := ps.p
	if q < len(ps.toks) && ps.toks[q].Kind == TokOpen {
		open = q
	} else {
		for q < len(ps.toks) && ps.toks[q].Kind == TokNewline {
			q++
		}
		if q < len(ps.toks) && ps.toks[q].Kind == TokOpen && ps.toks[q].Line > 0 && isAloneOnLine(ps.toks, q) && header[0] != "import" {
			open = q
		}
	}

	if open < 0 {
		lastHeader := ps.toks[ps.p-1]
		// trailing comment on the header line
		if ps.p < len(ps.toks) && ps.toks[ps.p].Kind == TokComment {
			seg.HeaderComment = ps.toks[ps.p].Text
			ps.p++
		}
		if header[0] == "import" || seg.Kind != KindSite {
			seg.Kind = KindDirective
			return seg, lineEnd(ps.src, lastHeader.End), nil
		}
		// Brace-less site: the rest of the file is its body.
		seg.Braces = false
		nodes, next, _, err := ps.parseBlock(ps.p, -1, false)
		if err != nil {
			return nil, 0, err
		}
		seg.Nodes = nodes
		ps.p = next
		return seg, len(ps.src), nil
	}

	nodes, next, openComment, err := ps.parseBlock(open+1, ps.toks[open].Line, true)
	if err != nil {
		return nil, 0, err
	}
	seg.Nodes, seg.HeaderComment = nodes, openComment
	ps.p = next
	return seg, lineEnd(ps.src, ps.toks[next-1].End), nil
}

func isAloneOnLine(toks []Token, i int) bool {
	if i > 0 && toks[i-1].Kind != TokNewline {
		return false
	}
	return i+1 >= len(toks) || toks[i+1].Kind == TokNewline || toks[i+1].Kind == TokComment
}

// parseBlock parses directives until the matching "}" (or EOF when
// requireClose is false). It returns the nodes, the index just past the
// closing brace, and any comment that sat on the opening-brace line.
func (ps *parser) parseBlock(p int, openLine int, requireClose bool) ([]*Node, int, string, error) {
	nodes := []*Node{}
	var cur *Node
	newlines := 0
	openComment := ""
	flush := func() {
		if cur != nil {
			nodes = append(nodes, cur)
			cur = nil
		}
	}
	for p < len(ps.toks) {
		t := ps.toks[p]
		switch t.Kind {
		case TokNewline:
			if cur != nil {
				flush()
				newlines = 1
			} else {
				newlines++
			}
			p++
		case TokComment:
			switch {
			case cur != nil:
				cur.Comment = t.Text
			case t.Line == openLine && len(nodes) == 0:
				openComment = t.Text
			default:
				nodes = append(nodes, &Node{Type: "comment", Text: t.Text, Blank: newlines >= 2 && len(nodes) > 0})
				newlines = 0
			}
			p++
		case TokWord:
			if cur == nil {
				cur = &Node{Type: "directive", Blank: newlines >= 2 && len(nodes) > 0}
				newlines = 0
			}
			cur.Tokens = append(cur.Tokens, t.Text)
			p++
		case TokOpen:
			if cur == nil {
				return nil, 0, "", &SyntaxError{Line: t.Line, Msg: "unexpected '{' (a block must follow a directive on the same line)"}
			}
			children, next, oc, err := ps.parseBlock(p+1, t.Line, true)
			if err != nil {
				return nil, 0, "", err
			}
			cur.Block = children
			if oc != "" {
				cur.Comment = oc
			}
			p = next
			// a comment after the closing brace stays with the node
			if p < len(ps.toks) && ps.toks[p].Kind == TokComment && ps.toks[p].Line == ps.toks[p-1].Line && cur.Comment == "" {
				cur.Comment = ps.toks[p].Text
				p++
			}
			flush()
			newlines = 0
		case TokClose:
			if !requireClose {
				return nil, 0, "", &SyntaxError{Line: t.Line, Msg: "unexpected '}'"}
			}
			flush()
			return nodes, p + 1, openComment, nil
		}
	}
	if requireClose {
		return nil, 0, "", &SyntaxError{Line: openLine, Msg: "missing closing '}' for the block opened here"}
	}
	flush()
	return nodes, p, openComment, nil
}

// ParseNodes parses a fragment of block content (directives without an
// enclosing block), e.g. text typed into a "raw" editor.
func ParseNodes(src string) ([]*Node, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	ps := &parser{src: src, toks: toks}
	nodes, _, _, err := ps.parseBlock(0, -1, false)
	return nodes, err
}

// ParseSegment parses text that must contain exactly one top-level block
// (optionally preceded by comments).
func ParseSegment(src string) (*Segment, error) {
	doc, err := Parse(src)
	if err != nil {
		return nil, err
	}
	segs := doc.Segments()
	if len(segs) != 1 {
		return nil, &SyntaxError{Line: 1, Msg: "expected exactly one block"}
	}
	seg := segs[0]
	seg.MarkDirty()
	return seg, nil
}
