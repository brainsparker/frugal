package browse

import (
	"strings"
	"testing"
)

var stripSeeds = []string{
	"", "<", ">", "<>", "</", "<<<>>>", "plain text",
	"<p>a</p><p>b</p>", "<P>A<BR>B</P>",
	"<script>alert(1)</script>after", "<SCRIPT src=x>", "<script", "<scrip", "</script>",
	"<style>.a{}</style>x", "<style", "<STYLE>p{}", "</style>",
	"<script>never closed <p>hidden", "<style><script></style>shown",
	"<a href=\"x>y\">link</a>", "<!-- comment -->text", "<!DOCTYPE html><html>",
	"a < b > c", "\t\n\r  spaced \t out  ", "<p>\xff\xfe invalid utf8</p>",
	"<div><span>nested</div></span>", "<img src=x onerror=y/>", "&amp; &lt;tag&gt;",
	"<scriptx>not a script</scriptx>visible", "<styles>tag</styles>visible",
}

func FuzzStripHTML(f *testing.F) {
	for _, s := range stripSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := StripHTML(in)
		if len(out) > len(in) {
			t.Fatalf("output longer than input: %d > %d", len(out), len(in))
		}
		if strings.ContainsAny(out, "<>\t\n\r") {
			t.Fatalf("output kept markup or raw whitespace: %q", out)
		}
		if strings.Contains(out, "  ") || strings.HasPrefix(out, " ") || strings.HasSuffix(out, " ") {
			t.Fatalf("whitespace not collapsed/trimmed: %q", out)
		}
		if again := StripHTML(out); again != out {
			t.Fatalf("not idempotent: %q -> %q", out, again)
		}
	})
}
