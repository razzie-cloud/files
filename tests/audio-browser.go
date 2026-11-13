//go:build ignore

// Run with go run tests/audio-browser.go; set CHROMIUM_EXECUTABLE_PATH if needed.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/razzie-cloud/files/tests/browser"
)

func audioFixture() []byte {
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(36+64000))
	out.WriteString("WAVEfmt ")
	binary.Write(&out, binary.LittleEndian, uint32(16))
	for _, v := range []uint16{1, 1} {
		binary.Write(&out, binary.LittleEndian, v)
	}
	for _, v := range []uint32{8000, 16000} {
		binary.Write(&out, binary.LittleEndian, v)
	}
	for _, v := range []uint16{2, 16} {
		binary.Write(&out, binary.LittleEndian, v)
	}
	out.WriteString("data")
	binary.Write(&out, binary.LittleEndian, uint32(64000))
	for i := 0; i < 32000; i++ {
		binary.Write(&out, binary.LittleEndian, int16(1000*math.Sin(float64(i)/8)))
	}
	return out.Bytes()
}
func pageHTML(public bool) string {
	base := "/directories/test"
	if public {
		base = "/s/capability"
	}
	var links string
	for _, v := range [][2]string{{"tone.wav", "audio"}, {"broken.mp3", "audio"}, {"photo.svg", "image"}} {
		links += fmt.Sprintf(`<a href="%s/download?path=%s&amp;inline=1" data-preview="%s" data-name="%s">Open %s</a> `, base, v[0], v[1], v[0], v[0])
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/static/pico.min.css"><link rel="stylesheet" href="/static/app.css"><script defer src="/static/htmx.min.js"></script><script defer src="/static/app.js"></script></head><body><main id="main">` + links + browser.Template("media-dialog") + `</main></body></html>`
}
func main() {
	browser.Main(func() {
		wav := audioFixture()
		b := browser.Start(func(r browser.Request) browser.Response {
			u, e := url.Parse(r.URL)
			if e != nil {
				panic(e)
			}
			if strings.HasPrefix(u.Path, "/static/") {
				return browser.Static(u.Path)
			}
			if strings.HasSuffix(u.Path, "/download") {
				switch u.Query().Get("path") {
				case "tone.wav":
					return browser.Response{Body: wav, Type: "audio/wav", Headers: map[string]string{"Accept-Ranges": "bytes"}}
				case "broken.mp3":
					return browser.Response{Body: []byte("not audio"), Type: "audio/mpeg"}
				default:
					return browser.Response{Body: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"/>`), Type: "image/svg+xml"}
				}
			}
			return browser.Response{Body: []byte(pageHTML(u.Path == "/shared")), Type: "text/html", Headers: map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'"}}
		})
		defer b.Close()
		for _, width := range []int{1280, 390} {
			b.Viewport(width, 844)
			for _, path := range []string{"/private", "/shared"} {
				b.Navigate("https://files.test" + path)
				b.Run(`
el('[data-preview]').focus();
el('[data-preview]').click();
const audio=el('audio');
await wait(()=>visible(audio)&&audio.readyState>=2);
assert(audio.paused,'audio must wait for Play');
await audio.play();
await wait(()=>audio.currentTime>0);
audio.pause();
audio.currentTime=2;
await wait(()=>!audio.seeking);
assert(Math.abs(audio.currentTime-2)<.1,'seek failed');
await click('Next →');
await wait(()=>visible(el('[data-preview-error]')));
await click('Next →');
await wait(()=>!visible(audio));
assert(audio.paused&&!audio.hasAttribute('src'),'navigation retained audio');
assert(!el('[data-preview-download]').getAttribute('href').includes('inline='),'download is inline');

`)
				b.Escape()
				b.Run(`
await wait(()=>!visible(el('dialog'))&&!el('audio').hasAttribute('src'));
el('[data-preview]').focus();
el('[data-preview]').click();
await el('audio').play();

`)
				b.Escape()
				b.Run(`
await wait(()=>{const a=el('audio');
return a.paused&&!a.hasAttribute('src')});
assert(el('[data-preview]')===document.activeElement,'focus not restored');
el('[data-preview]').click();
await el('audio').play();
await htmx.ajax('GET','/fragment',{target:'#main',swap:'innerHTML'});
await wait(()=>!visible(el('dialog')));
el('[data-preview]').click();
await wait(()=>visible(el('[data-preview-audio]')));

`)
				b.Escape()
			}
		}
		b.AssertNoErrors()
		fmt.Println("PASS: audio playback, seeking, errors, navigation, cleanup, focus, HTMX; private/shared; desktop/mobile")
	})
}
