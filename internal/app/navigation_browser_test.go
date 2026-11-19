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
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/tests/browser"
)

// Uses the production templates/controllers with disposable responses and no S3,
// gateway or database. Enable with CHROMIUM_EXECUTABLE_PATH.
func TestPanelNavigationBrowser(t *testing.T) {
	if os.Getenv("CHROMIUM_EXECUTABLE_PATH") == "" {
		t.Skip("set CHROMIUM_EXECUTABLE_PATH for browser acceptance")
	}
	s, err := New(config.Config{ShareOrigin: "https://shared.test"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Directory{ID: testOwner, Name: "Photos", Bucket: "photos", Revision: 1}
	b := browser.Start(func(request browser.Request) browser.Response {
		u, err := url.Parse(request.URL)
		if err != nil {
			panic(err)
		}
		if strings.HasPrefix(u.Path, "/static/") {
			return browser.Static(u.Path)
		}
		if u.Host == "www.gstatic.com" {
			return browser.Response{Type: "text/javascript", Body: []byte("/* Cast hardware is outside this fixture. */")}
		}
		if u.Host == "auth.test" {
			return browser.Response{Type: "text/html", Body: []byte("<!doctype html><html><body><h1>Sign in</h1></body></html>")}
		}
		if u.Query().Has("expired") {
			return browser.Response{Status: 303, Headers: map[string]string{"Location": "https://auth.test/login?return=" + url.QueryEscape(u.RequestURI())}}
		}
		v := view{Page: "browse", Message: "Photos", Directory: d, Writable: true, Path: u.Query().Get("path"), ShareDirectory: true,
			Entries: []storage.Entry{{Name: "nested", Path: "nested", Directory: true}, {Name: "song.mp3", Path: "song.mp3"}}}
		base := "/directories/" + testOwner
		if u.Host == "shared.test" {
			base, v.Page, v.Writable, v.ShareURL = "/s/capability", "shared", false, "/s/capability"
		}
		v.Parent, v.Crumbs, v.Next = base, []crumb{{Name: "Photos", URL: base}}, base+"?offset=10"
		switch u.Path {
		case "/":
			v.Page, v.Directory, v.Directories = "home", nil, []store.Directory{*d}
		case "/shares":
			v.Page, v.Directory = "share-list", nil
		case "/connections":
			v.Page, v.Directory, v.Connections = "connections", nil, []store.Connection{{ID: testOwner, Name: "Storage"}}
		case "/connections/new":
			v.Page, v.Directory, v.Connection = "connections", nil, &store.Connection{Provider: "Other"}
		case base + "/edit":
			v.Page = "directory-form"
		case base + "/shares":
			v.Page = "shares"
		case base + "/rename":
			v.Page, v.FileAction = "file-action", &fileActionView{Kind: "rename", Name: "song.mp3", Source: "song.mp3"}
		case base + "/move":
			v.Page, v.FileAction = "file-action", &fileActionView{Kind: "move", Name: "song.mp3", Source: "song.mp3"}
		}
		r := httptest.NewRequest(request.Method, request.URL, nil)
		for key, value := range request.Headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		if u.Path == "/complete" {
			s.done(w, r, base)
		} else if u.Query().Has("fail") {
			s.fail(w, r, store.ErrNotFound, 404)
		} else {
			s.render(w, r, v)
		}
		headers := map[string]string{
			"Content-Security-Policy": "default-src 'self'; script-src 'self' https://www.gstatic.com/cv/js/sender/ https://www.gstatic.com/cast/sdk/libs/sender/ https://www.gstatic.com/eureka/clank/; connect-src 'self' https://shared.test; style-src 'self'; img-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
			"Cache-Control":           "no-store", "Referrer-Policy": "same-origin",
		}
		for key, values := range w.Header() {
			headers[key] = strings.Join(values, ", ")
		}
		return browser.Response{Status: w.Code, Body: w.Body.Bytes(), Type: "text/html", Headers: headers}
	})
	defer b.Close()
	waitNavigation := func(expected string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			ready := false
			func() {
				// A full history restoration destroys the old execution context.
				defer func() { _ = recover() }()
				ready = string(b.Eval(`location.href===`+browser.Quote(expected)+`&&document.readyState==='complete'`)) == "true"
			}()
			if ready {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("history did not restore %s", expected)
	}
	screenshot := func(name string) {
		if dir := os.Getenv("FILES_BROWSER_SCREENSHOTS"); dir != "" {
			var result struct{ Data string }
			if err := json.Unmarshal(b.Call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}), &result); err != nil {
				t.Fatal(err)
			}
			data, err := base64.StdEncoding.DecodeString(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, theme := range []string{"light", "dark"} {
		b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []any{map[string]string{"name": "prefers-color-scheme", "value": theme}}})
		for _, width := range []int{390, 768, 1440} {
			b.Viewport(width, 900)
			for _, origin := range []string{"https://files.test", "https://shared.test"} {
				base := "/directories/" + testOwner
				if strings.Contains(origin, "shared") {
					base = "/s/capability"
				}
				b.Navigate(origin + base)
				b.Run(`
window.shell=document.querySelector('.topbar'); window.marker={};
const publicPage=location.hostname==='shared.test';
const pendingForm=document.createElement('form');
pendingForm.setAttribute('hx-post','/pending');
pendingForm.setAttribute('hx-target','#main');
el('#main').append(pendingForm);
htmx.process(pendingForm);
let pendingXHR;
pendingForm.addEventListener('htmx:beforeSend',e=>pendingXHR=e.detail.xhr);
pendingForm.requestSubmit();
el('a.name-cell[data-entry-open][hx-boost]').click();
assert(pendingXHR&&pendingXHR.readyState===0,'navigation did not abort the older panel write');
await wait(()=>location.search==='?path=nested'&&el('h1').textContent===(publicPage?'Photos':'nested'));
await wait(()=>document.activeElement===el('h1'));
assert(window.marker&&window.shell===el('.topbar'),'folder navigation replaced the document');
assert(el('h1').getAttribute('tabindex')==='-1','heading focus unavailable');
assert(htmx.config.historyCacheSize===0,'history snapshots enabled');
el('a[data-entry-open]').click();
await wait(()=>location.search==='');
assert(window.shell===el('.topbar'),'parent navigation replaced the document');
el('nav[aria-label=Pages] a').click();
await wait(()=>location.search==='?offset=10');
assert(window.shell===el('.topbar'),'pagination replaced the document');
assert(!el('[data-preview]').hasAttribute('hx-boost'),'preview boosted');
assert([...document.querySelectorAll('a[href*="/download"]')].every(a=>!a.hasAttribute('hx-boost')),'download boosted');
assert(document.documentElement.scrollWidth<=innerWidth,'horizontal overflow');
`)
				b.Eval(`setTimeout(()=>history.back(),0)`)
				waitNavigation(origin + base)
				b.Eval(`setTimeout(()=>history.forward(),0)`)
				waitNavigation(origin + base + "?offset=10")
				screenshot(fmt.Sprintf("%s-%d-%s", theme, width, strings.TrimPrefix(origin, "https://")))
				if strings.Contains(origin, "shared") {
					b.Run(`assert(!document.querySelector('.sidebar'),'public share has management sidebar');`)
					continue
				}
				b.Run(`
window.shell=el('.topbar');
window.requests=0;
document.addEventListener('htmx:beforeRequest',()=>window.requests++);
for (const action of ['Rename','Move']) {
  el('a[aria-label="'+action+'"]').click();
  await wait(()=>el('#file-action-dialog').open);
  const oldURL=location.href;
  const requestsBeforeCancel=window.requests;
  el('#file-action-dialog [data-cancel-file-action]').click();
  await wait(()=>!el('#file-action-dialog').open);
  await frame();
  assert(location.href===oldURL&&window.shell===el('.topbar'),'Cancel navigated');
  assert(window.requests===requestsBeforeCancel,'Cancel sent a navigation request');
}
el('a[aria-label="Share folder"]').click();
await wait(()=>location.pathname.endsWith('/shares'));
assert(window.shell===el('.topbar'),'share navigation replaced document');
el('.page-heading a[hx-boost]').click();
await wait(()=>!location.pathname.endsWith('/shares'));
el('nav[aria-label="Directory settings"] a').click();
await wait(()=>location.pathname.endsWith('/edit'));
el('nav[aria-label=Primary] a[href="/shares"]').click();
await wait(()=>location.pathname==='/shares');
assert(el('.sidebar [aria-current="page"]').getAttribute('href')==='/shares','Shares active state');
assert(el('h1').textContent==='Shares','Shares view');
el('nav[aria-label=Primary] a[href="/connections"]').click();
await wait(()=>location.pathname==='/connections');
el('#add-connection').click();
await wait(()=>el('#connection-dialog').open);
el('[data-close-connection]').click();
await wait(()=>!el('#connection-dialog').open);
el('nav[aria-label=Primary] a[href="/"]').click();
await wait(()=>location.pathname==='/');
el('.directory-card a[hx-boost]').click();
await wait(()=>location.pathname.startsWith('/directories/'));
assert(window.shell===el('.topbar'),'settings navigation replaced document');
`)
			}
		}
	}
	b.Navigate("https://files.test/directories/" + testOwner)
	b.Run(`
let completed=false;
document.addEventListener('htmx:beforeRequest',e=>{if(e.detail.elt.id==='main')completed=true});
const save=document.createElement('form'); save.setAttribute('hx-post','/complete'); save.setAttribute('hx-target','#main');
el('#main').append(save); htmx.process(save); save.requestSubmit();
await wait(()=>completed&&el('h1').textContent==='Photos');
`)
	b.Run(`
window.shell=el('.topbar');
const failure=document.createElement('a');
failure.href=location.pathname+'?fail=1';
failure.setAttribute('hx-boost','true');
failure.setAttribute('hx-target','#main');
el('#main').append(failure); htmx.process(failure); failure.click();
await wait(()=>el('h1').textContent==='Unable to complete the request');
assert(window.shell===el('.topbar'),'error replaced the document shell');
assert(el('[role=alert]').textContent.includes('Not found'),'missing safe error message');
el('[role=alert] a[href="/"]').click();
await wait(()=>location.pathname==='/');
`)
	b.Run(`
const expired=document.createElement('a');
expired.href='/directories/` + testOwner + `?expired=1';
expired.setAttribute('hx-boost','true');
expired.setAttribute('hx-target','#main');
el('#main').append(expired); htmx.process(expired);
setTimeout(()=>expired.click(),0);
`)
	waitNavigation("https://auth.test/login?return=" + url.QueryEscape("/directories/"+testOwner+"?expired=1"))
	b.Run(`assert(el('h1').textContent==='Sign in','expired session did not leave a usable sign-in page');`)
	b.AssertNoErrors()
}
