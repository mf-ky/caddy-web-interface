package caddyfile

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRoundTripUserFile(t *testing.T) {
	for _, src := range []string{load(t, "user.caddyfile"), strings.ReplaceAll(load(t, "user.caddyfile"), "\r\n", "\n")} {
		doc, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.String(); got != src {
			t.Fatalf("round trip changed the file:\n%q\nvs\n%q", got, src)
		}
		segs := doc.Segments()
		if len(segs) != 9 {
			t.Fatalf("want 9 segments, got %d", len(segs))
		}
		if segs[0].Kind != KindGlobal || segs[0].Comments[0] != "# Global options block - sets up your Porkbun keys for Let's Encrypt" {
			t.Fatalf("global segment wrong: %+v", segs[0])
		}
		if segs[1].Key() != "site:dns.example.com" || segs[1].Comments[0] != "# DNS filter" {
			t.Fatalf("first site wrong: %+v", segs[1])
		}
		nas := segs[3]
		rp := nas.Nodes[0]
		if rp.Tokens[0] != "reverse_proxy" || len(rp.Block) != 6 {
			t.Fatalf("nas reverse_proxy block: %d nodes %+v", len(rp.Block), rp)
		}
		if doc.Indent != "    " {
			t.Fatalf("indent %q", doc.Indent)
		}
	}
}

func TestEditKeepsOtherBlocks(t *testing.T) {
	src := strings.ReplaceAll(load(t, "user.caddyfile"), "\r\n", "\n")
	doc, _ := Parse(src)
	segs := doc.Segments()
	music := segs[4]
	music.Nodes[0].Tokens[1] = "http://192.168.1.31:4000"
	music.MarkDirty()
	out := doc.String()
	if !strings.Contains(out, "# Music\nmusic.example.com {\n    reverse_proxy http://192.168.1.31:4000\n}\n") {
		t.Fatalf("edit not rendered:\n%s", out)
	}
	if strings.Replace(out, "192.168.1.31", "192.168.1.30", 1) != src {
		t.Fatal("other parts of the file changed")
	}
}

func TestAppendRemove(t *testing.T) {
	src := "a.example.com {\n\treverse_proxy localhost:1\n}\n\n# B\nb.example.com {\n\trespond hi\n}\n"
	doc, _ := Parse(src)
	seg := &Segment{Kind: KindSite, Comments: []string{"# C"}, Header: []string{"c.example.com"},
		Nodes: []*Node{{Type: "directive", Tokens: []string{"file_server"}}}}
	doc.Append(seg)
	want := src + "\n# C\nc.example.com {\n\tfile_server\n}\n"
	if doc.String() != want {
		t.Fatalf("append:\n%q", doc.String())
	}
	doc.Remove(doc.Segments()[1])
	want = "a.example.com {\n\treverse_proxy localhost:1\n}\n\n# C\nc.example.com {\n\tfile_server\n}\n"
	if doc.String() != want {
		t.Fatalf("remove:\n%q", doc.String())
	}
}

func TestTrickySyntax(t *testing.T) {
	src := `{
	email me@example.com # inline
}

(common) {
	encode gzip
}

import common

example.com, www.example.com {
	@api path /api/*
	handle @api {
		reverse_proxy "localhost:9000" {
			header_up X-Test "a b {c}"
		}
	} # after close
	respond <<HTML
		<p>{ not a block }</p>
		HTML 200
	header / `+"`raw # not comment`"+`
}
`
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.String() != src {
		t.Fatal("round trip")
	}
	segs := doc.Segments()
	kinds := []SegmentKind{KindGlobal, KindSnippet, KindDirective, KindSite}
	for i, k := range kinds {
		if segs[i].Kind != k {
			t.Fatalf("seg %d kind %s want %s", i, segs[i].Kind, k)
		}
	}
	site := segs[3]
	if strings.Join(site.Addresses(), "|") != "example.com|www.example.com" {
		t.Fatal(site.Addresses())
	}
	if site.Nodes[1].Comment != "# after close" {
		t.Fatalf("comment after close: %+v", site.Nodes[1])
	}
	// Re-formatting the edited site must stay parseable and equivalent.
	site.MarkDirty()
	again, err := Parse(doc.String())
	if err != nil {
		t.Fatalf("reformatted output does not parse: %v\n%s", err, doc.String())
	}
	if FormatNodes(again.Segments()[3].Nodes, "\t", 1) != FormatNodes(site.Nodes, "\t", 1) {
		t.Fatal("reformat changed structure")
	}
}

func TestBracelessSite(t *testing.T) {
	src := "localhost\n\nrespond \"hello\"\n"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Segments()[0]
	if s.Kind != KindSite || len(s.Nodes) != 1 {
		t.Fatalf("%+v", s)
	}
	s.MarkDirty()
	if doc.String() != "localhost {\n\trespond \"hello\"\n}\n" {
		t.Fatalf("%q", doc.String())
	}
}

func TestErrors(t *testing.T) {
	for _, src := range []string{"a.com {\n", "}\n", "a.com {\n respond \"x\n}\n"} {
		if _, err := Parse(src); err == nil {
			t.Fatalf("expected error for %q", src)
		}
	}
}

func TestRedact(t *testing.T) {
	src := strings.ReplaceAll(load(t, "user.caddyfile"), "\r\n", "\n")
	red := RedactText(src)
	if strings.Contains(red, `"redacted"`) {
		t.Fatalf("secret leaked:\n%s", red)
	}
	if !strings.Contains(red, "api_key "+Mask) || !strings.Contains(red, "reverse_proxy localhost:8080") {
		t.Fatalf("redaction wrong:\n%s", red)
	}
}

func TestResolveRawRejectsInjection(t *testing.T) {
	bad := []*Node{{Type: "directive", Tokens: []string{"respond", "hi\n}\nevil.com {"}}}
	if _, err := ResolveRaw(bad); err == nil {
		t.Fatal("token injection accepted")
	}
	raw := "header_up Host {upstream_hostport}\ntransport http {\n\ttls_insecure_skip_verify\n}"
	nodes, err := ResolveRaw([]*Node{{Raw: &raw}})
	if err != nil || len(nodes) != 2 || nodes[1].Block == nil {
		t.Fatalf("raw parse: %v %+v", err, nodes)
	}
}
