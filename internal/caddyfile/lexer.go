// Package caddyfile is a lossless reader/writer for Caddyfiles.
//
// Caddy's own parser throws away comments and formatting, which is fine for
// Caddy but not for CaddyWeb: we promise the user that the file on disk stays
// something they can read and edit by hand. So this package keeps the original
// bytes of every block it does not touch, and only re-formats the blocks that
// were edited through the UI.
package caddyfile

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind classifies a lexed token.
type TokenKind int

const (
	TokWord    TokenKind = iota // a bare word, quoted string, backtick string or heredoc
	TokOpen                     // a lone "{"
	TokClose                    // a lone "}"
	TokComment                  // "# ..." up to (not including) the newline
	TokNewline                  // "\n"
)

// Token is one lexical unit. Text is the raw source text (quotes included),
// so writing tokens back out reproduces what the user typed.
type Token struct {
	Kind  TokenKind
	Text  string
	Line  int // 1-based line the token starts on
	Start int // byte offset of the first character
	End   int // byte offset just past the last character
}

// SyntaxError is returned for input the lexer or parser cannot make sense of.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// Lex splits src into tokens following the Caddyfile rules closely enough for
// round-tripping: whitespace separates tokens, newlines are significant, "#"
// starts a comment only at the start of a token, quotes and backticks group
// text, a trailing backslash continues a line, and "<<MARKER" starts a heredoc.
func Lex(src string) ([]Token, error) {
	var toks []Token
	line := 1
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			toks = append(toks, Token{Kind: TokNewline, Text: "\n", Line: line, Start: i, End: i + 1})
			line++
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c >= utf8.RuneSelf && isUnicodeSpace(src[i:]):
			// Caddy splits on any Unicode space (NBSP, …), so do we
			_, size := utf8.DecodeRuneInString(src[i:])
			i += size
			continue
		case c == '\\' && i+1 < n && (src[i+1] == '\n' || (src[i+1] == '\r' && i+2 < n && src[i+2] == '\n')):
			// line continuation: swallow the backslash and the newline
			if src[i+1] == '\r' {
				i += 3
			} else {
				i += 2
			}
			line++
			continue
		case c == '#':
			start := i
			for i < n && src[i] != '\n' {
				i++
			}
			toks = append(toks, Token{Kind: TokComment, Text: strings.TrimRight(src[start:i], "\r"), Line: line, Start: start, End: i})
			continue
		}

		// Heredoc: <<MARKER at the end of a line.
		if c == '<' && strings.HasPrefix(src[i:], "<<") {
			if tok, next, lines, ok := lexHeredoc(src, i, line); ok {
				toks = append(toks, tok)
				i = next
				line += lines
				continue
			}
		}

		// A word. Like Caddy, a quote or backtick only starts a quoted token
		// at the very beginning of a word; elsewhere it is an ordinary character.
		start := i
		startLine := line
		if c == '"' || c == '`' {
			q := c
			i++
			for i < n && src[i] != q {
				if q == '"' && src[i] == '\\' && i+1 < n && src[i+1] == '"' {
					i += 2
					continue
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i >= n {
				return nil, &SyntaxError{Line: startLine, Msg: "unterminated quoted string"}
			}
			i++ // closing quote
			toks = append(toks, Token{Kind: TokWord, Text: src[start:i], Line: startLine, Start: start, End: i})
			continue
		}
		for i < n {
			c = src[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			if c >= utf8.RuneSelf && isUnicodeSpace(src[i:]) {
				break
			}
			if c == '\\' && i+1 < n && (src[i+1] == '\n' || (src[i+1] == '\r' && i+2 < n && src[i+2] == '\n')) {
				break // line continuation
			}
			i++
		}
		text := src[start:i]
		kind := TokWord
		switch text {
		case "{":
			kind = TokOpen
		case "}":
			kind = TokClose
		}
		toks = append(toks, Token{Kind: kind, Text: text, Line: startLine, Start: start, End: i})
	}
	return toks, nil
}

func isMarkerChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// lexHeredoc reads "<<MARKER\n ... \n  MARKER" starting at src[i].
func lexHeredoc(src string, i, line int) (Token, int, int, bool) {
	j := i + 2
	for j < len(src) && isMarkerChar(src[j]) {
		j++
	}
	marker := src[i+2 : j]
	if marker == "" {
		return Token{}, 0, 0, false
	}
	k := j
	for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\r') {
		k++
	}
	if k >= len(src) || src[k] != '\n' {
		return Token{}, 0, 0, false
	}
	// scan lines until one whose trimmed content is the marker
	pos := k + 1
	lines := 1
	for pos <= len(src) {
		end := strings.IndexByte(src[pos:], '\n')
		var ln string
		if end < 0 {
			ln = src[pos:]
			end = len(src) - pos
		} else {
			ln = src[pos : pos+end]
		}
		// The closing line is the marker, optionally followed by more tokens
		// on the same line (e.g. "HTML 200").
		trimmed := strings.TrimLeft(ln, " \t")
		if strings.HasPrefix(trimmed, marker) {
			rest := trimmed[len(marker):]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' {
				stop := pos + (len(ln) - len(trimmed)) + len(marker)
				return Token{Kind: TokWord, Text: src[i:stop], Line: line, Start: i, End: stop}, stop, lines, true
			}
		}
		if pos+end >= len(src) {
			break
		}
		pos += end + 1
		lines++
	}
	return Token{}, 0, 0, false
}

func isUnicodeSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}
