package api

import (
	"bytes"
	"net/http"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"

	"sprinkler/docs"
)

// docsPage renders docs/API.md once, on first use. goldmark escapes raw
// HTML in the source by default.
var docsPage = sync.OnceValues(func() ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var b bytes.Buffer
	b.WriteString(docsHead)
	if err := md.Convert(docs.API, &b); err != nil {
		return nil, err
	}
	b.WriteString("</main></body></html>\n")
	return b.Bytes(), nil
})

const docsHead = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sprinkler API</title>
<style>
:root{color-scheme:light dark;--fg:#1d2327;--bg:#fff;--muted:#f2f4f5;--line:#d8dde0;--link:#1a6fb5}
@media (prefers-color-scheme:dark){:root{--fg:#e3e6e8;--bg:#16191b;--muted:#22272a;--line:#353b3f;--link:#6db3f2}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.55 system-ui,sans-serif}
main{max-width:52rem;margin:0 auto;padding:1rem 16px 3rem}
a{color:var(--link)}
h1,h2,h3{line-height:1.25;margin-top:2em}h1{margin-top:.5em}
h2{border-bottom:1px solid var(--line);padding-bottom:.25em}
code{font:.9em ui-monospace,monospace;background:var(--muted);padding:.1em .3em;border-radius:4px}
pre{background:var(--muted);padding:.75rem 1rem;border-radius:6px;overflow-x:auto}
pre code{background:none;padding:0}
table{border-collapse:collapse;display:block;overflow-x:auto;margin:1em 0}
th,td{border:1px solid var(--line);padding:.35em .6em;text-align:left;vertical-align:top}
th{background:var(--muted)}
</style></head><body><main>
`

// apiDocs serves the API reference as HTML. It needs no login: the
// content is the same as docs/API.md in the public repository.
func (s *Server) apiDocs(w http.ResponseWriter, _ *http.Request) {
	page, err := docsPage()
	if err != nil {
		s.log.Error("api docs", "err", err)
		http.Error(w, "docs unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

// apiDocsMarkdown serves the raw Markdown source.
func (s *Server) apiDocsMarkdown(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(docs.API)
}
