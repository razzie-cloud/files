// Package browser runs disposable Chromium fixtures through its DevTools protocol.
// It needs a Chromium executable, but no Playwright, Node, or downloaded Go tools.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

type Request struct{ URL, Method, PostData string }
type Response struct {
	Body    []byte
	Type    string
	Status  int
	Headers map[string]string
}
type message struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type Browser struct {
	conn    *websocket.Conn
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	profile string
	mu      sync.Mutex
	pending map[int64]chan message
	next    atomic.Int64
	errors  []string
	closed  chan struct{}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
func Root() string {
	_, path, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(path)))
}
func Read(path string) []byte { b, e := os.ReadFile(filepath.Join(Root(), path)); check(e); return b }
func Template(name string) string {
	s := strings.SplitN(string(Read("internal/web/templates.html")), `{{define "`+name+`"}}`, 2)
	if len(s) != 2 {
		panic("template missing: " + name)
	}
	return strings.SplitN(s[1], "{{end}}", 2)[0]
}
func Start(route func(Request) Response) *Browser {
	binary := os.Getenv("PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH")
	if binary == "" {
		binary = os.Getenv("CHROMIUM_EXECUTABLE_PATH")
	}
	if binary == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
			if path, e := exec.LookPath(name); e == nil {
				binary = path
				break
			}
		}
	}
	if binary == "" {
		panic("set CHROMIUM_EXECUTABLE_PATH to an installed Chromium browser")
	}
	profile, e := os.MkdirTemp("", "files-browser-*")
	check(e)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	b := &Browser{profile: profile, cancel: cancel, pending: map[int64]chan message{}, closed: make(chan struct{})}
	b.cmd = exec.CommandContext(ctx, binary, "--headless", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", "--user-data-dir="+profile, "--autoplay-policy=no-user-gesture-required", "about:blank")
	if e = b.cmd.Start(); e != nil {
		b.Close()
		panic(e)
	}
	// Every startup failure must also reap Chromium and remove its temporary profile.
	started := false
	defer func() {
		if !started {
			b.Close()
		}
	}()
	var port string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, e := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if e == nil {
			port = strings.SplitN(string(raw), "\n", 2)[0]
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if port == "" {
		panic("Chromium did not start; check CHROMIUM_EXECUTABLE_PATH")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res, e := client.Get("http://127.0.0.1:" + port + "/json/list")
	check(e)
	var targets []struct {
		URL  string `json:"webSocketDebuggerUrl"`
		Type string `json:"type"`
	}
	e = json.NewDecoder(res.Body).Decode(&targets)
	res.Body.Close()
	check(e)
	var endpoint string
	for _, t := range targets {
		if t.Type == "page" {
			endpoint = t.URL
			break
		}
	}
	if endpoint == "" {
		panic("Chromium has no page target")
	}
	b.conn, e = websocket.Dial(endpoint, "", "http://127.0.0.1:"+port)
	check(e)
	b.conn.MaxPayloadBytes = 16 << 20
	go b.receive(route)
	b.Call("Runtime.enable", nil)
	b.Call("Page.enable", nil)
	b.Call("Fetch.enable", map[string]any{"patterns": []any{map[string]any{"urlPattern": "*"}}})
	started = true
	return b
}
func (b *Browser) receive(route func(Request) Response) {
	defer close(b.closed)
	for {
		var m message
		if e := websocket.JSON.Receive(b.conn, &m); e != nil {
			return
		}
		if m.ID != 0 {
			b.mu.Lock()
			ch := b.pending[m.ID]
			b.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method == "Runtime.exceptionThrown" {
			b.mu.Lock()
			b.errors = append(b.errors, string(m.Params))
			b.mu.Unlock()
		}
		if m.Method == "Fetch.requestPaused" {
			go func(raw json.RawMessage) {
				defer func() {
					if r := recover(); r != nil {
						b.mu.Lock()
						b.errors = append(b.errors, fmt.Sprint(r))
						b.mu.Unlock()
					}
				}()
				var p struct {
					ID      string  `json:"requestId"`
					Request Request `json:"request"`
				}
				check(json.Unmarshal(raw, &p))
				response := route(p.Request)
				if response.Status == 0 {
					response.Status = 200
				}
				headers := make([]any, 0)
				if response.Type != "" {
					headers = append(headers, map[string]string{"name": "Content-Type", "value": response.Type})
				}
				for k, v := range response.Headers {
					headers = append(headers, map[string]string{"name": k, "value": v})
				}
				b.Call("Fetch.fulfillRequest", map[string]any{"requestId": p.ID, "responseCode": response.Status, "responseHeaders": headers, "body": base64.StdEncoding.EncodeToString(response.Body)})
			}(m.Params)
		}
	}
}
func (b *Browser) Call(method string, params any) json.RawMessage {
	id := b.next.Add(1)
	ch := make(chan message, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	check(websocket.JSON.Send(b.conn, map[string]any{"id": id, "method": method, "params": params}))
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			panic(fmt.Sprintf("%s: %s", method, m.Error))
		}
		return m.Result
	case <-b.closed:
		panic("Chromium connection closed")
	case <-time.After(12 * time.Second):
		panic("Chromium command timed out: " + method)
	}
}
func (b *Browser) Eval(js string) json.RawMessage {
	raw := b.Call("Runtime.evaluate", map[string]any{"expression": js, "awaitPromise": true, "returnByValue": true})
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	check(json.Unmarshal(raw, &r))
	if len(r.Exception) > 0 {
		panic(fmt.Sprintf("%s\nExpression: %s", r.Exception, js))
	}
	return r.Result.Value
}
func (b *Browser) Run(js string) { b.Eval(`(async()=>{` + Helpers + js + `})()`) }
func (b *Browser) Viewport(width, height int) {
	b.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false})
}
func (b *Browser) Navigate(url string) {
	b.Call("Page.navigate", map[string]any{"url": url})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if string(b.Eval(`location.href===`+Quote(url)+`&&document.readyState==='complete'`)) == "true" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("page navigation timed out")
}
func (b *Browser) Escape() {
	for _, event := range []string{"keyDown", "keyUp"} {
		b.Call("Input.dispatchKeyEvent", map[string]any{"type": event, "key": "Escape", "code": "Escape", "windowsVirtualKeyCode": 27})
	}
	b.Eval(`new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))`)
}
func (b *Browser) AssertNoErrors() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.errors) > 0 {
		panic(strings.Join(b.errors, "\n"))
	}
}
func (b *Browser) Close() {
	if b.conn != nil {
		b.conn.Close()
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Wait()
	}
	if b.profile != "" {
		os.RemoveAll(b.profile)
	}
}
func Quote(v string) string { b, e := json.Marshal(v); check(e); return string(b) }
func Static(path string) Response {
	body := Read("internal/web/static/" + filepath.Base(path))
	kind := "text/javascript"
	if strings.HasSuffix(path, ".css") {
		kind = "text/css"
	}
	return Response{Body: body, Type: kind}
}
func Main(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, r)
			os.Exit(1)
		}
	}()
	fn()
}

// Keep assertions inside the real browser; wait on observable behavior rather than sleeps.
const Helpers = `
const assert=(ok,message)=>{if(!ok)throw Error(message||'assertion failed')};
const wait=async(fn)=>{const end=Date.now()+5000;while(!fn()){if(Date.now()>end)throw Error('timed out: '+fn);await new Promise(r=>setTimeout(r,20));}};
const el=s=>{const e=document.querySelector(s);assert(e,'missing '+s);return e};
const visible=e=>!!(e.getClientRects().length)&&getComputedStyle(e).visibility!=='hidden';
const button=name=>{const e=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===name&&visible(e));assert(e,'missing button '+name);return e};
const frame=()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
const click=async name=>{const e=button(name);assert(!e.disabled,'disabled '+name);e.click();await frame();};
const status=text=>wait(()=>el('[data-cast-status]').textContent.includes(text));
const swap=html=>{el('#main').innerHTML=html;document.dispatchEvent(new Event('htmx:afterSwap'));};
`
