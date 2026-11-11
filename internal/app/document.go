package app

import (
	"bytes"
	"context"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

const maxDocumentBytes = 2 << 20

type documentView struct {
	Name, Text, Message string
	HTML                template.HTML
}

func (s *Server) serveDocument(w http.ResponseWriter, r *http.Request, fs storage.FS, key string, markdown bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	// No scripts, forms, embedded resources, or navigation of the parent page.
	// Only our local stylesheets may load. External links require an explicit click.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'none'; sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox; frame-ancestors 'self'")
	v := documentView{Name: path.Base(key)}
	respond := func(status int, message string) {
		v.Message = message
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_ = s.templates.ExecuteTemplate(w, "document-preview", v)
		}
	}
	info, err := fs.Stat(ctx, key)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	if info.Directory {
		respond(http.StatusUnsupportedMediaType, "Folders cannot be previewed as documents.")
		return
	}
	if info.Size > maxDocumentBytes {
		respond(http.StatusRequestEntityTooLarge, "This file exceeds the 2 MiB text preview limit. Download it to read the full file.")
		return
	}
	in, err := fs.Open(ctx, key, -1, -1)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer in.Close()
	if in.Size != info.Size {
		s.fail(w, r, store.ErrConflict, http.StatusConflict)
		return
	}
	source, err := io.ReadAll(io.LimitReader(in, maxDocumentBytes+1))
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	if len(source) > maxDocumentBytes {
		respond(http.StatusRequestEntityTooLarge, "This file exceeds the 2 MiB text preview limit. Download it to read the full file.")
		return
	}
	if info.Size >= 0 && int64(len(source)) != info.Size {
		s.fail(w, r, store.ErrConflict, http.StatusConflict)
		return
	}
	source = bytes.TrimPrefix(source, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(source) || bytes.IndexFunc(source, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f'
	}) >= 0 {
		respond(http.StatusUnsupportedMediaType, "This file is binary or uses an unsupported text encoding. Download it to open it locally. Text previews require UTF-8.")
		return
	}
	if markdown {
		// Goldmark's safe defaults omit raw HTML and dangerous URLs. Our
		// renderer additionally removes images and relative capability links.
		md := goldmark.New(
			goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(documentLinks{}, 100))),
		)
		var out bytes.Buffer
		if err := md.Convert(source, &out); err != nil {
			respond(http.StatusUnprocessableEntity, "Unable to format this Markdown file. Download it to read the source.")
			return
		}
		v.HTML = template.HTML(out.String())
	} else {
		v.Text = string(source)
	}
	respond(http.StatusOK, "")
}

type documentLinks struct{}

// Never resolve document URLs against a download/capability URL. Relative links
// remain readable text; external links open without a referrer or window.opener.
func documentHref(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" || u.Scheme == "mailto" {
		return u.String()
	}
	if u.Scheme == "" && u.Host == "" && u.Path == "" && u.RawQuery == "" && u.Fragment != "" {
		return "#" + url.PathEscape(u.Fragment)
	}
	return ""
}

func (documentLinks) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, err := w.WriteString(html.EscapeString(string(node.Text(source))))
			return ast.WalkSkipChildren, err
		}
		return ast.WalkContinue, nil
	})
	reg.Register(ast.KindLink, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		href := documentHref(string(node.(*ast.Link).Destination))
		if href == "" {
			return ast.WalkContinue, nil
		}
		if entering {
			target := ` target="_blank" rel="noopener noreferrer"`
			if strings.HasPrefix(href, "#") {
				target = ""
			}
			_, err := w.WriteString(`<a href="` + html.EscapeString(href) + `"` + target + `>`)
			return ast.WalkContinue, err
		}
		_, err := w.WriteString("</a>")
		return ast.WalkContinue, err
	})
	reg.Register(ast.KindAutoLink, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link := node.(*ast.AutoLink)
		destination := string(link.URL(source))
		if link.AutoLinkType == ast.AutoLinkEmail {
			destination = "mailto:" + strings.TrimPrefix(destination, "mailto:")
		}
		label := html.EscapeString(string(link.Label(source)))
		if href := documentHref(destination); href != "" {
			label = `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + label + `</a>`
		}
		_, err := w.WriteString(label)
		return ast.WalkSkipChildren, err
	})
}
