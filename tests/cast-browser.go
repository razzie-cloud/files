//go:build ignore

// Run with go run tests/cast-browser.go; set CHROMIUM_EXECUTABLE_PATH if needed.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/razzie-cloud/files/tests/browser"
)

func pageHTML() string {
	return `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/static/pico.min.css"><link rel="stylesheet" href="/static/app.css"><script defer src="/static/cast-controller.js"></script><script defer src="/static/cast.js"></script></head><body><main id="main"><p>Home</p></main>` + browser.Template("cast-controller") + `</body></html>`
}
func main() {
	browser.Main(func() {
		var mu sync.Mutex
		state := map[string]int{"create": 0, "remove": 0, "sdk": 0, "heads": 0}
		bump := func(k string) { mu.Lock(); state[k]++; mu.Unlock() }
		count := func(k string) int { mu.Lock(); defer mu.Unlock(); return state[k] }
		b := browser.Start(func(r browser.Request) browser.Response {
			if strings.HasPrefix(r.URL, "https://www.gstatic.com/") {
				bump("sdk")
				return browser.Response{Body: []byte(fakeSDK), Type: "text/javascript"}
			}
			u, e := url.Parse(r.URL)
			if e != nil {
				panic(e)
			}
			if strings.HasPrefix(u.Path, "/static/") {
				return browser.Static(u.Path)
			}
			if strings.HasSuffix(u.Path, "/cast") {
				bump("create")
				form, e := url.ParseQuery(r.PostData)
				if e != nil {
					panic(e)
				}
				mode := form.Get("mode")
				data := map[string]any{"mode": "slideshow", "title": "Empty folder", "expires": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)}
				if form.Get("path") != "empty" {
					data["mode"] = mode
					data["title"] = "Photos"
					data["media"] = map[string]string{"name": "movie.mp4", "url": "https://shared.example.test/s/test/media?path=movie.mp4", "mime": "video/mp4"}
					data["cleanup"] = "/remove/test"
					entries := []any{}
					for i := 0; i < 3; i++ {
						entries = append(entries, map[string]string{"name": fmt.Sprintf("%d.jpg", i), "url": fmt.Sprintf("https://shared.example.test/s/test/media?path=%d.jpg", i), "mime": "image/jpeg"})
					}
					data["entries"] = entries
				}
				raw, e := json.Marshal(data)
				if e != nil {
					panic(e)
				}
				return browser.Response{Body: raw, Type: "application/json"}
			}
			if strings.Contains(r.URL, "/s/test/media?") {
				if r.Method != "HEAD" {
					panic("media preflight must be HEAD")
				}
				bump("heads")
				return browser.Response{Headers: map[string]string{"Access-Control-Allow-Origin": "https://files.example.test"}}
			}
			if strings.Contains(u.Path, "/remove/test") {
				bump("remove")
				return browser.Response{Status: 204}
			}
			return browser.Response{Body: []byte(pageHTML()), Type: "text/html", Headers: map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self' https://www.gstatic.com/cv/js/sender/; connect-src 'self' https://shared.example.test; style-src 'self'"}}
		})
		defer b.Close()
		b.Viewport(1024, 768)
		b.Navigate("https://files.example.test/")
		if count("sdk") != 0 {
			panic("SDK loaded without casting entry points")
		}
		b.Run(`
swap('<button data-cast="/directories/test/cast" data-path="" data-mode="slideshow" disabled>Cast slideshow</button>');
await wait(()=>!button('Cast slideshow').disabled);

`)
		if count("sdk") != 1 {
			panic("SDK load count")
		}
		b.Run(`
await click('Cast slideshow');

`)
		if count("create") != 0 {
			panic("share created before picker selection")
		}
		b.Run(`
fakeCast.cancel();
await status('canceled');

`)
		if count("create") != 0 {
			panic("canceled picker created a share")
		}
		b.Run(`
await click('Cast slideshow');
fakeCast.choose();
await status('0.jpg');
const loaded=fakeCast.loads[0];
assert(loaded.media.contentId.endsWith('path=0.jpg'));
assert(loaded.media.streamType==='OTHER');
assert(loaded.media.metadata.metadataType===4);
await click('Pause');
await click('Next');
await status('1.jpg');
assert(filesCast.operation.playing===false);
swap('<p>Another directory</p>');
for(let i=0;
i<4;
i++)document.dispatchEvent(new Event('htmx:afterSwap'));
assert(document.querySelectorAll('#cast-controller').length===1);
await click('Next');
await status('2.jpg');
assert(fakeCast.loads.length===3);

`)
		if count("create") != 1 || count("sdk") != 1 {
			panic("duplicate shares or SDK loads")
		}
		b.Viewport(390, 844)
		b.Run(`
const bounds=button('Next').getBoundingClientRect();
assert(bounds.left>=0&&bounds.right<=innerWidth&&bounds.top>=0&&bounds.bottom<=innerHeight,'Next outside viewport');
fakeCast.holdEnd=true;
await click('Stop');
await status('stopped');
await wait(()=>fakeCast.ended.length===1);
assert(fakeCast.ended[0].stopCasting===true);
assert(fakeCast.current!==null);
swap('<button data-cast="/directories/test/cast" data-path="movie.mp4" data-mode="video">Cast video</button>');
assert(button('Cast video').disabled);
fakeCast.finishEnd();
fakeCast.holdEnd=false;
await wait(()=>!button('Cast video').disabled);
assert(fakeCast.current===null);

`)
		if count("remove") != 1 || count("heads") != 3 {
			panic("slideshow cleanup or media preflight count")
		}
		b.Run(`
swap('<button data-cast="/directories/test/cast" data-path="movie.mp4" data-mode="video">Cast video</button>');
await click('Cast video');
fakeCast.choose();
await status('movie.mp4');
const loaded=fakeCast.loads.at(-1);
assert(loaded.media.streamType==='BUFFERED');
assert(loaded.media.metadata.metadataType===1);
Object.assign(fakeCast.player,{duration:100,currentTime:25,isPaused:true,canSeek:true,isMediaLoaded:true,playerState:'PLAYING'});
fakeCast.remoteChanged();
await click('Play');
assert(fakeCast.toggled===true);
const seek=el('[data-cast-seek]');
seek.value='50';
seek.dispatchEvent(new Event('change',{bubbles:true}));
assert(fakeCast.sought===true);
const volume=el('[data-cast-volume]');
volume.value='0.5';
volume.dispatchEvent(new Event('change',{bubbles:true}));
assert(fakeCast.volume===true);
fakeCast.player.playerState='IDLE';
fakeCast.player.isMediaLoaded=false;
fakeCast.remoteChanged();
await status('Video ended');
swap('<button data-cast="/directories/test/cast" data-path="" data-mode="slideshow">Cast slideshow</button>');
await click('Cast slideshow');
fakeCast.choose();
await status('0.jpg');
const timer=filesCast.operation.timer;
await click('Previous');
assert(filesCast.operation.timer===timer);
const pickers=fakeCast.pickers,session=fakeCast.current.getSessionId(),loads=fakeCast.loads.length;
await click('Cast slideshow');
await wait(()=>fakeCast.loads.length===loads+1);
assert(fakeCast.pickers===pickers);
assert(fakeCast.current.getSessionId()===session);

`)
		removed := count("remove")
		b.Run(`
const loads=fakeCast.loads.length,ended=fakeCast.ended.length;
const entry=el('[data-cast]');
entry.dataset.cast='/s/existing/cast';
entry.dataset.path='empty';
await click('Cast slideshow');
await status('no JPEG or PNG');
await wait(()=>fakeCast.ended.length===ended+1&&!fakeCast.current);
assert(fakeCast.loads.length===loads);

`)
		if count("remove") != removed+1 {
			panic("only the replaced owner share should be revoked")
		}
		created := count("create")
		b.Run(`
const ended=fakeCast.ended.length;
await click('Cast slideshow');
await click('Stop');
fakeCast.choose();
await wait(()=>fakeCast.ended.length===ended+1&&!fakeCast.current);

`)
		if count("create") != created {
			panic("late picker selection created a share")
		}
		b.Run(`
await click('Cast slideshow');
fakeCast.fail();
await status('Unable to connect');

`)
		for _, scope := range []string{"idle", "foreign-media", "foreign-session"} {
			b.Run(`
const entry=el('[data-cast]');
entry.dataset.cast='/directories/test/cast';
entry.dataset.path='';
await click('Cast slideshow');
fakeCast.choose();
await status('0.jpg');
const ended=fakeCast.ended.length,scope=
` + browser.Quote(scope) + `
;
if(scope==='idle')fakeCast.current.getMediaSession().media=null;
else if(scope==='foreign-media')fakeCast.current.getMediaSession().media={contentId:'https://unrelated.example/video.mp4'};
else fakeCast.current=newSession();
await click('Stop');
await status('stopped');
if(scope==='idle'){await wait(()=>fakeCast.ended.length===ended+1&&!fakeCast.current)}else{assert(fakeCast.ended.length===ended);
assert(fakeCast.current!==null);
fakeCast.current=null;
fakeCast.sessionState='SESSION_ENDED';
fakeCast.emit('session',{sessionState:'SESSION_ENDED'});
}
`)
		}
		b.AssertNoErrors()
		fmt.Println("PASS SDK session stop/restart, connected-session replacement, late selection, controls, HTMX, and empty public folders")
	})
}

const fakeSDK = `
window.fakeCast = {loads: [], stopped: 0, ended: [], pickers: 0, current: null, sessionState: 'NO_SESSION', listeners: {}};
const fake = window.fakeCast;
fake.emit = (type, data) => (fake.listeners[type] || []).forEach(handler => handler(data));
function newSession() {
 const id = 'test-' + fake.pickers;
const media = {media: null, stop(request, yes) { fake.stopped++;
yes();
}};
return {getSessionId: () => id, getMediaSession: () => media,
  loadMedia(request) {fake.loads.push(request);
media.media = request.media;
return Promise.resolve();
}};
}
const context = {
 setOptions(options) { fake.options = options;
},
 getCastState: () => fake.current ? 'CONNECTED' : 'NOT_CONNECTED',
 getSessionState: () => fake.sessionState,
 addEventListener(type, handler) { (fake.listeners[type] ||= []).push(handler);
},
 getCurrentSession: () => fake.current,
 requestSession() {
  fake.pickers++;
if (fake.current) return Promise.reject('session_error');
return new Promise((yes, no) => {
   fake.choose = () => {fake.current = newSession();
fake.sessionState = 'SESSION_STARTED';
fake.emit('session', {sessionState: fake.sessionState});
yes();
};
fake.cancel = () => no('cancel');
fake.fail = () => no('session_error');
});
},
 endCurrentSession(stopCasting) {
  if (!fake.current || fake.sessionState === 'SESSION_ENDING') return;
const session = fake.current;
fake.ended.push({id: session.getSessionId(), stopCasting});
fake.sessionState = 'SESSION_ENDING';
fake.emit('session', {sessionState: fake.sessionState});
fake.finishEnd = () => {
   if (fake.current !== session) return;
fake.current = null;
fake.sessionState = 'SESSION_ENDED';
fake.emit('session', {sessionState: fake.sessionState});
fake.emit('cast', {});
};
if (!fake.holdEnd) queueMicrotask(fake.finishEnd);
}
};
window.cast = {framework: {CastContext: {getInstance: () => context},
 CastState: {NO_DEVICES_AVAILABLE: 'NONE'}, SessionState: {SESSION_ENDING: 'SESSION_ENDING', SESSION_ENDED: 'SESSION_ENDED'},
 CastContextEventType: {CAST_STATE_CHANGED: 'cast', SESSION_STATE_CHANGED: 'session'},
 RemotePlayerEventType: {ANY_CHANGE:'change'}, RemotePlayer: function() {fake.player=this;
this.volumeLevel=1;
this.isPaused=false;
},
 RemotePlayerController: function() {this.addEventListener = (_,fn) => fake.remoteChanged=fn;
this.playOrPause = () => fake.toggled=true;
this.seek = () => fake.sought=true;
this.setVolumeLevel = () => fake.volume=true;
}}};
window.chrome = {cast: {AutoJoinPolicy: {ORIGIN_SCOPED:'ORIGIN'}, ErrorCode: {CANCEL:'cancel'}, media: {
 DEFAULT_MEDIA_RECEIVER_APP_ID:'CC1AD845', StreamType: {BUFFERED:'BUFFERED',OTHER:'OTHER'}, PlayerState: {IDLE:'IDLE'},
 MediaInfo:function(url,mime) {this.contentId=url;
this.contentType=mime;
},
 PhotoMediaMetadata:function() {this.metadataType=4;
}, MovieMediaMetadata:function() {this.metadataType=1;
},
 LoadRequest:function(media) {this.media=media;
}, StopRequest:function() {}}}};
window.__onGCastApiAvailable(true);

`
