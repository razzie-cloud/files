package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/tests/browser"
)

// Exercises the real list/revoke handlers and templates with owner-scoped
// in-memory metadata, without S3, a gateway or database credentials.
func TestShareListBrowser(t *testing.T) {
	if os.Getenv("CHROMIUM_EXECUTABLE_PATH") == "" {
		t.Skip("set CHROMIUM_EXECUTABLE_PATH for browser acceptance")
	}
	repo := shareListFixture()
	s, err := New(config.Config{Origin: "https://files.test", ShareOrigin: "https://shared.test"}, repo, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	var mu sync.Mutex
	var failures []string
	b := browser.Start(func(request browser.Request) browser.Response {
		r := httptest.NewRequest(request.Method, request.URL, strings.NewReader(request.PostData))
		for k, v := range request.Headers {
			r.Header.Set(k, v)
		}
		r.Header.Set("X-Auth-Request-User", testOwner)
		r.Header.Set("X-Auth-Request-Name", "razzie")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code >= 400 {
			mu.Lock()
			failures = append(failures, fmt.Sprint(w.Code, " ", r.URL.Path))
			mu.Unlock()
		}
		headers := map[string]string{}
		for k, v := range w.Header() {
			headers[k] = strings.Join(v, ", ")
		}
		return browser.Response{Status: w.Code, Body: w.Body.Bytes(), Type: w.Header().Get("Content-Type"), Headers: headers}
	})
	defer b.Close()
	b.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `window.issues=[];addEventListener('securitypolicyviolation',e=>issues.push(e.violatedDirective));`})
	screenshot := func(name string) {
		if dir := os.Getenv("FILES_SHARES_SCREENSHOT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			var shot struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(b.Call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": true}), &shot); err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(shot.Data)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+".png"), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, theme := range []string{"light", "dark"} {
		b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []any{map[string]string{"name": "prefers-color-scheme", "value": theme}}})
		for _, width := range []int{320, 390, 768, 1440} {
			b.Viewport(width, 900)
			b.Navigate("https://files.test/")
			b.Run(`await wait(()=>window.htmx);window.shell=el('.topbar');el('.sidebar a[href="/shares"]').click();await wait(()=>location.pathname==='/shares'&&document.querySelector('.share-card'));assert(window.shell===el('.topbar'),'shell replaced');assert(el('.sidebar [aria-current="page"]').getAttribute('href')==='/shares','active navigation');assert(document.querySelectorAll('h1').length===1);assert(document.querySelectorAll('.share-card').length===2,'cross-directory shares');assert(document.documentElement.scrollWidth<=innerWidth,'overflow');assert(issues.length===0,'CSP');assert([...document.images].every(i=>i.complete&&i.naturalWidth>0),'image');assert(htmx.config.historyCacheSize===0);for(const card of document.querySelectorAll('.share-card')){const h=card.querySelector('h2').getBoundingClientRect(), badge=card.querySelector('.badge').getBoundingClientRect();assert(Math.abs(h.top-badge.top)<6,'heading/badge alignment');assert(card.querySelector('form').action.includes('/directories/'),'scoped revoke');}assert([...document.querySelectorAll('a[target="_blank"]')].every(a=>!a.hasAttribute('hx-boost')),'share link boosted');`)
			if width == 390 || width == 1440 {
				screenshot(fmt.Sprintf("shares-%s-%d", theme, width))
			}
			// Direct URLs and a creation round trip keep the overview accessible.
			b.Navigate("https://files.test/directories/" + testOwner + "/shares?path=" + url.QueryEscape("Projects/<brand>"))
			b.Run(`await wait(()=>window.htmx);assert(el('input[name="path"]').value==='Projects/<brand>');assert(document.documentElement.scrollWidth<=innerWidth,'creation overflow');window.shell=el('.topbar');[...document.querySelectorAll('a')].find(a=>a.textContent==='View all shares').click();await wait(()=>location.pathname==='/shares');assert(window.shell===el('.topbar'),'creation return replaced shell');`)
		}
	}
	// History restores fresh HTML with snapshots disabled.
	b.Run(`el('.sidebar a[href="/"]').click();await wait(()=>location.pathname==='/');`)
	waitURL := func(expected string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			ready := false
			func() {
				defer func() { _ = recover() }()
				ready = string(b.Eval(`location.href===`+browser.Quote(expected)+`&&document.readyState==='complete'`)) == "true"
			}()
			if ready {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("history restoration timed out", expected)
	}
	for _, direction := range []string{"back", "forward", "back"} {
		b.Eval(`setTimeout(()=>history.` + direction + `(),0)`)
		expected := "https://files.test/shares"
		if direction == "forward" {
			expected = "https://files.test/"
		}
		waitURL(expected)
	}
	b.Run(`assert(el('.sidebar [aria-current="page"]').getAttribute('href')==='/shares');assert(el('h1').textContent==='Shares');`)
	// Real POSTs remove both active and expired metadata and return to the list.
	b.Run(`for(let i=0;i<2;i++){el('.share-card button').click();await wait(()=>document.querySelectorAll('.share-card').length===1-i);assert(location.pathname==='/shares','revoke left overview');}assert(el('h2').textContent==='No shares yet');assert(document.documentElement.scrollWidth<=innerWidth);assert(issues.length===0);`)
	for _, theme := range []string{"light", "dark"} {
		b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []any{map[string]string{"name": "prefers-color-scheme", "value": theme}}})
		for _, width := range []int{320, 390, 768, 1440} {
			b.Viewport(width, 900)
			b.Navigate("https://files.test/shares")
			b.Run(`assert(el('h2').textContent==='No shares yet');assert(document.documentElement.scrollWidth<=innerWidth,'empty overflow');assert(issues.length===0,'empty CSP');if(innerWidth<=760)assert(el('.sidebar').getBoundingClientRect().height<110,'empty navigation stretched');`)
			if width == 390 || width == 1440 {
				screenshot(fmt.Sprintf("shares-empty-%s-%d", theme, width))
			}
		}
	}
	b.AssertNoErrors()
	mu.Lock()
	defer mu.Unlock()
	if len(failures) > 0 {
		t.Fatal(failures)
	}
}
