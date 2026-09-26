package caddyfile

import (
	"strings"
)

// SegmentKind says what a top-level piece of the Caddyfile is.
type SegmentKind string

const (
	KindGlobal     SegmentKind = "global"     // the leading "{ ... }" global options block
	KindSite       SegmentKind = "site"       // "example.com { ... }"
	KindSnippet    SegmentKind = "snippet"    // "(name) { ... }"
	KindNamedRoute SegmentKind = "namedroute" // "&(name) { ... }"
	KindDirective  SegmentKind = "directive"  // a top-level line such as "import common.caddy"
)

// Node is one line inside a block: a directive (with optional sub-block) or
// a standalone comment. Tokens hold raw text exactly as written.
type Node struct {
	Type    string   `json:"type"` // "directive" or "comment"
	Tokens  []string `json:"tokens,omitempty"`
	Text    string   `json:"text,omitempty"`    // comment text including "#", for Type=="comment"
	Block   []*Node  `json:"block"`             // nil when the directive has no { } block
	Comment string   `json:"comment,omitempty"` // trailing "# ..." on the same line
	Blank   bool     `json:"blank,omitempty"`   // a blank line precedes this node
	// Raw is only used on input from the UI: Caddyfile text that the server
	// parses and splices in place of this node.
	Raw *string `json:"raw,omitempty"`
}

// Segment is one top-level block together with the comment lines directly
// above it (which CaddyWeb shows as the card's title/notes).
type Segment struct {
	Kind          SegmentKind `json:"kind"`
	Comments      []string    `json:"comments"`      // leading comment lines, raw ("# Pi-hole")
	Header        []string    `json:"header"`        // addresses / "(name)" / directive tokens
	HeaderComment string      `json:"headerComment"` // "# ..." after the opening brace
	Nodes         []*Node     `json:"nodes"`
	Braces        bool        `json:"-"` // false only for a brace-less single site file

	raw   string // original text; "" when the segment was built or edited
	dirty bool
}

// Part is either a Segment or filler text (blank lines, detached comments).
type Part struct {
	Seg    *Segment
	Filler string
}

// Document is a parsed Caddyfile that can be written back losslessly.
type Document struct {
	Parts  []*Part
	Indent string // indentation unit detected from the file ("\t" or spaces)
	CRLF   bool   // the file uses Windows line endings
}

// Segments returns the segments in file order.
func (d *Document) Segments() []*Segment {
	var out []*Segment
	for _, p := range d.Parts {
		if p.Seg != nil {
			out = append(out, p.Seg)
		}
	}
	return out
}

// MarkDirty forces the segment to be re-formatted on output.
func (s *Segment) MarkDirty() { s.dirty = true; s.raw = "" }

// Raw returns the original text of an unedited segment.
func (s *Segment) Raw() string { return s.raw }

// Key identifies a segment across versions of the file (e.g. live vs draft).
func (s *Segment) Key() string {
	switch s.Kind {
	case KindGlobal:
		return "global"
	case KindDirective:
		return "directive:" + strings.Join(s.Header, " ")
	}
	return string(s.Kind) + ":" + strings.Join(normalizeHeader(s.Header), " ")
}

func normalizeHeader(h []string) []string {
	var out []string
	for _, t := range h {
		for _, p := range strings.Split(t, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// Addresses returns the site addresses with commas removed.
func (s *Segment) Addresses() []string { return normalizeHeader(s.Header) }

// String writes the document back out. Untouched segments and filler are
// reproduced byte-for-byte.
func (d *Document) String() string {
	var b strings.Builder
	for _, p := range d.Parts {
		if p.Seg == nil {
			b.WriteString(p.Filler)
			continue
		}
		b.WriteString(p.Seg.Text(d.Indent))
	}
	out := b.String()
	if d.CRLF {
		out = strings.ReplaceAll(strings.ReplaceAll(out, "\r\n", "\n"), "\n", "\r\n")
	}
	return out
}

// Text renders one segment: its original bytes if unchanged, otherwise a
// freshly formatted version using indent.
func (s *Segment) Text(indent string) string {
	if !s.dirty && s.raw != "" {
		return s.raw
	}
	return s.Format(indent)
}

// Format renders the segment in canonical form regardless of dirtiness.
func (s *Segment) Format(indent string) string {
	if indent == "" {
		indent = "\t"
	}
	var b strings.Builder
	for _, c := range s.Comments {
		b.WriteString(c)
		b.WriteString("\n")
	}
	if s.Kind == KindDirective {
		b.WriteString(strings.Join(s.Header, " "))
		if s.HeaderComment != "" {
			b.WriteString(" " + s.HeaderComment)
		}
		b.WriteString("\n")
		return b.String()
	}
	if s.Kind == KindGlobal {
		b.WriteString("{")
	} else {
		b.WriteString(joinHeader(s.Header))
		b.WriteString(" {")
	}
	if s.HeaderComment != "" {
		b.WriteString(" " + s.HeaderComment)
	}
	b.WriteString("\n")
	writeNodes(&b, s.Nodes, indent, 1)
	b.WriteString("}\n")
	return b.String()
}

func joinHeader(h []string) string {
	return strings.Join(h, " ")
}

// FormatNodes renders a list of nodes at the given depth.
func FormatNodes(nodes []*Node, indent string, depth int) string {
	var b strings.Builder
	writeNodes(&b, nodes, indent, depth)
	return b.String()
}

func writeNodes(b *strings.Builder, nodes []*Node, indent string, depth int) {
	pad := strings.Repeat(indent, depth)
	for i, n := range nodes {
		if n.Blank && i > 0 {
			b.WriteString("\n")
		}
		if n.Type == "comment" {
			b.WriteString(pad + n.Text + "\n")
			continue
		}
		b.WriteString(pad + strings.Join(n.Tokens, " "))
		if n.Block != nil {
			b.WriteString(" {")
		}
		if n.Comment != "" {
			b.WriteString(" " + n.Comment)
		}
		b.WriteString("\n")
		if n.Block != nil {
			writeNodes(b, n.Block, indent, depth+1)
			b.WriteString(pad + "}\n")
		}
	}
}

// Remove deletes a segment and tidies up the blank lines around it.
func (d *Document) Remove(seg *Segment) bool {
	for i, p := range d.Parts {
		if p.Seg != seg {
			continue
		}
		d.Parts = append(d.Parts[:i], d.Parts[i+1:]...)
		// Merge neighbouring filler so we don't accumulate empty lines.
		if i > 0 && i < len(d.Parts) && d.Parts[i-1].Seg == nil && d.Parts[i].Seg == nil {
			a, b := d.Parts[i-1].Filler, d.Parts[i].Filler
			switch {
			case strings.TrimSpace(b) == "":
			case strings.TrimSpace(a) == "":
				d.Parts[i-1].Filler = b
			default:
				d.Parts[i-1].Filler = collapseBlank(a + b)
			}
			d.Parts = append(d.Parts[:i], d.Parts[i+1:]...)
		} else if i < len(d.Parts) && d.Parts[i].Seg == nil && strings.TrimSpace(d.Parts[i].Filler) == "" && i == 0 {
			d.Parts = append(d.Parts[:i], d.Parts[i+1:]...)
		}
		return true
	}
	return false
}

// collapseBlank limits runs of blank lines to a single blank line.
func collapseBlank(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// Append adds a segment at the end of the file, separated by one blank line.
func (d *Document) Append(seg *Segment) {
	text := d.String()
	sep := ""
	switch {
	case text == "":
	case strings.HasSuffix(text, "\n\n"):
	case strings.HasSuffix(text, "\n"):
		sep = "\n"
	default:
		sep = "\n\n"
	}
	if sep != "" {
		d.Parts = append(d.Parts, &Part{Filler: sep})
	}
	d.Parts = append(d.Parts, &Part{Seg: seg})
}

// InsertAfter adds seg right after `after` (or at the top, after the global
// block, when after is nil).
func (d *Document) InsertAfter(after *Segment, seg *Segment) {
	idx := -1
	if after != nil {
		for i, p := range d.Parts {
			if p.Seg == after {
				idx = i
			}
		}
	}
	if idx < 0 {
		d.Append(seg)
		return
	}
	rest := append([]*Part{{Filler: "\n"}, {Seg: seg}}, d.Parts[idx+1:]...)
	d.Parts = append(d.Parts[:idx+1], rest...)
}

// Replace swaps old for new in place, keeping surrounding filler. A segment
// built from UI input has no raw text, so it is formatted on output; one
// taken from another parsed document keeps its original bytes.
func (d *Document) Replace(old, new *Segment) bool {
	for _, p := range d.Parts {
		if p.Seg == old {
			p.Seg = new
			return true
		}
	}
	return false
}

// Prepend puts seg at the very top of the file (used for a new global
// options block, which Caddy requires to come first).
func (d *Document) Prepend(seg *Segment) {
	parts := []*Part{{Seg: seg}}
	if len(d.Parts) > 0 {
		parts = append(parts, &Part{Filler: "\n"})
	}
	d.Parts = append(parts, d.Parts...)
}

// Lines returns the 1-based first and last line of each segment.
func (d *Document) Lines() map[*Segment][2]int {
	out := map[*Segment][2]int{}
	line := 1
	for _, p := range d.Parts {
		var text string
		if p.Seg != nil {
			text = p.Seg.Text(d.Indent)
		} else {
			text = p.Filler
		}
		n := strings.Count(text, "\n")
		if p.Seg != nil {
			end := line + n - 1
			if !strings.HasSuffix(text, "\n") {
				end = line + n
			}
			out[p.Seg] = [2]int{line, end}
		}
		line += n
	}
	return out
}

// Global returns the global options segment, if any.
func (d *Document) Global() *Segment {
	for _, s := range d.Segments() {
		if s.Kind == KindGlobal {
			return s
		}
	}
	return nil
}

// Clone returns a deep copy of the segment (dirty state and raw preserved).
func (s *Segment) Clone() *Segment {
	c := *s
	c.Comments = append([]string(nil), s.Comments...)
	c.Header = append([]string(nil), s.Header...)
	c.Nodes = CloneNodes(s.Nodes)
	return &c
}

// CloneNodes deep-copies a node list.
func CloneNodes(nodes []*Node) []*Node {
	if nodes == nil {
		return nil
	}
	out := make([]*Node, len(nodes))
	for i, n := range nodes {
		c := *n
		c.Tokens = append([]string(nil), n.Tokens...)
		if n.Block != nil {
			c.Block = CloneNodes(n.Block)
		}
		out[i] = &c
	}
	return out
}

// DetectIndent picks the indentation unit most used in src (tab or N spaces).
func DetectIndent(src string) string {
	tabs, spaces := 0, map[int]int{}
	for _, ln := range strings.Split(src, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(ln, "\t") {
			tabs++
			continue
		}
		k := len(ln) - len(strings.TrimLeft(ln, " "))
		if k > 0 {
			spaces[k]++
		}
	}
	// the smallest indentation width in use is the unit
	totalSpaces := 0
	unit := 0
	for k, c := range spaces {
		totalSpaces += c
		if unit == 0 || k < unit {
			unit = k
		}
	}
	if totalSpaces > tabs && unit > 0 {
		if unit > 8 {
			unit = 4
		}
		return strings.Repeat(" ", unit)
	}
	return "\t"
}
