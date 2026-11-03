"""Run with Python + Playwright/Chromium; no Node packages or build step.
Exercises the real SDK adapter with a fake SDK, HTTP APIs, and HTMX swaps.
"""
import asyncio
import json
from pathlib import Path
from datetime import datetime, timezone, timedelta
from urllib.parse import parse_qs
from playwright.async_api import async_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
STATIC = ROOT / 'internal/web/static'
FAKE_SDK = r'''
window.fakeCast = {loads: [], stopped: 0, ended: [], pickers: 0, current: null, sessionState: 'NO_SESSION', listeners: {}};
const fake = window.fakeCast;
fake.emit = (type, data) => (fake.listeners[type] || []).forEach(handler => handler(data));
function newSession() {
 const id = 'test-' + fake.pickers;
 const media = {media: null, stop(request, yes) { fake.stopped++; yes(); }};
 return {getSessionId: () => id, getMediaSession: () => media,
  loadMedia(request) {fake.loads.push(request); media.media = request.media; return Promise.resolve();}};
}
const context = {
 setOptions(options) { fake.options = options; },
 getCastState: () => fake.current ? 'CONNECTED' : 'NOT_CONNECTED',
 getSessionState: () => fake.sessionState,
 addEventListener(type, handler) { (fake.listeners[type] ||= []).push(handler); },
 getCurrentSession: () => fake.current,
 requestSession() {
  fake.pickers++;
  if (fake.current) return Promise.reject('session_error');
  return new Promise((yes, no) => {
   fake.choose = () => {fake.current = newSession();fake.sessionState = 'SESSION_STARTED';fake.emit('session', {sessionState: fake.sessionState});yes();};
   fake.cancel = () => no('cancel');
   fake.fail = () => no('session_error');
  });
 },
 endCurrentSession(stopCasting) {
  if (!fake.current || fake.sessionState === 'SESSION_ENDING') return;
  const session = fake.current;
  fake.ended.push({id: session.getSessionId(), stopCasting});
  fake.sessionState = 'SESSION_ENDING';fake.emit('session', {sessionState: fake.sessionState});
  fake.finishEnd = () => {
   if (fake.current !== session) return;
   fake.current = null;fake.sessionState = 'SESSION_ENDED';
   fake.emit('session', {sessionState: fake.sessionState});fake.emit('cast', {});
  };
  if (!fake.holdEnd) queueMicrotask(fake.finishEnd);
 }
};
window.cast = {framework: {CastContext: {getInstance: () => context},
 CastState: {NO_DEVICES_AVAILABLE: 'NONE'}, SessionState: {SESSION_ENDING: 'SESSION_ENDING', SESSION_ENDED: 'SESSION_ENDED'},
 CastContextEventType: {CAST_STATE_CHANGED: 'cast', SESSION_STATE_CHANGED: 'session'},
 RemotePlayerEventType: {ANY_CHANGE:'change'}, RemotePlayer: function() {fake.player=this;this.volumeLevel=1;this.isPaused=false;},
 RemotePlayerController: function() {this.addEventListener = (_,fn) => fake.remoteChanged=fn;this.playOrPause = () => fake.toggled=true;
 this.seek = () => fake.sought=true; this.setVolumeLevel = () => fake.volume=true;}}};
window.chrome = {cast: {AutoJoinPolicy: {ORIGIN_SCOPED:'ORIGIN'}, ErrorCode: {CANCEL:'cancel'}, media: {
 DEFAULT_MEDIA_RECEIVER_APP_ID:'CC1AD845', StreamType: {BUFFERED:'BUFFERED',OTHER:'OTHER'}, PlayerState: {IDLE:'IDLE'},
 MediaInfo:function(url,mime) {this.contentId=url;this.contentType=mime;},
 PhotoMediaMetadata:function() {this.metadataType=4;}, MovieMediaMetadata:function() {this.metadataType=1;},
 LoadRequest:function(media) {this.media=media;}, StopRequest:function() {}}}};
window.__onGCastApiAvailable(true);
'''

def page_html(casting):
    templates = (ROOT / 'internal/web/templates.html').read_text()
    panel = templates.split('{{define "cast-controller"}}')[1].split('{{end}}')[0]
    button = '<button data-cast="/directories/test/cast" data-path="" data-mode="slideshow" disabled>Cast slideshow</button>' if casting else '<p>Home</p>'
    return ('<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/static/pico.min.css">'
            '<link rel="stylesheet" href="/static/app.css">'
            '<script defer src="/static/cast-controller.js"></script><script defer src="/static/cast.js"></script>'
            '</head><body><main id="main">'+button+'</main>'+panel+'</body></html>')

async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(args=['--no-sandbox'])
        page = await browser.new_page(viewport={'width': 1024, 'height': 768})
        page.set_default_timeout(5000)
        errors, requests, state = [], [], {'create': 0, 'remove': 0, 'sdk': 0}
        page.on('pageerror', lambda error: errors.append(str(error)))
        async def route(request_route):
            request = request_route.request
            if request.url.startswith('https://www.gstatic.com/'):
                state['sdk'] += 1
                await request_route.fulfill(body=FAKE_SDK, content_type='text/javascript')
            elif '/static/' in request.url:
                filename = request.url.split('/static/')[1]
                await request_route.fulfill(path=str(STATIC / filename))
            elif request.url.endswith('/cast'):
                state['create'] += 1
                form = parse_qs(request.post_data)
                mode = form['mode'][0]
                if form.get('path') == ['empty']:
                    await request_route.fulfill(json={'mode': 'slideshow', 'title': 'Empty folder',
                        'expires': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})
                    return
                await request_route.fulfill(json={'mode': mode, 'title': 'Photos', 'media': {'name': 'movie.mp4', 'url': 'https://shared.example.test/s/test/media?path=movie.mp4', 'mime': 'video/mp4'}, 'expires': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
                    'cleanup': '/remove/test', 'entries': [{'name': f'{i}.jpg', 'url': f'https://shared.example.test/s/test/media?path={i}.jpg', 'mime': 'image/jpeg'} for i in range(3)]})
            elif '/s/test/media?' in request.url:
                requests.append(request)
                assert request.method == 'HEAD'
                await request_route.fulfill(headers={'Access-Control-Allow-Origin': 'https://files.example.test'}, body='')
            elif '/remove/test' in request.url:
                state['remove'] += 1
                await request_route.fulfill(status=204)
            else:
                await request_route.fulfill(body=page_html(False), content_type='text/html', headers={
                    'Content-Security-Policy': "default-src 'self'; script-src 'self' https://www.gstatic.com/cv/js/sender/; connect-src 'self' https://shared.example.test; style-src 'self'"})
        await page.route('**/*', route)
        await page.goto('https://files.example.test/')
        assert state['sdk'] == 0, 'SDK loaded without casting entry points'
        await page.evaluate('''() => {
          document.querySelector('#main').innerHTML='<button data-cast="/directories/test/cast" data-path="" data-mode="slideshow" disabled>Cast slideshow</button>';
          document.dispatchEvent(new Event('htmx:afterSwap'));
        }''')
        await expect(page.get_by_role('button', name='Cast slideshow')).to_be_enabled()
        assert state['sdk'] == 1
        await page.get_by_role('button', name='Cast slideshow').click()
        assert state['create'] == 0, 'Share created before picker selection'
        await page.evaluate('fakeCast.cancel()')
        await expect(page.locator('[data-cast-status]')).to_contain_text('canceled')
        assert state['create'] == 0
        await page.get_by_role('button', name='Cast slideshow').click()
        await page.evaluate('fakeCast.choose()')
        await expect(page.locator('[data-cast-status]')).to_contain_text('0.jpg')
        assert state['create'] == 1
        loaded = await page.evaluate('fakeCast.loads[0]')
        assert loaded['media']['contentId'].endswith('path=0.jpg')
        assert loaded['media']['streamType'] == 'OTHER'
        assert loaded['media']['metadata']['metadataType'] == 4
        await page.get_by_role('button', name='Pause', exact=True).click()
        await page.get_by_role('button', name='Next', exact=True).click()
        await expect(page.locator('[data-cast-status]')).to_contain_text('1.jpg')
        assert await page.evaluate('filesCast.operation.playing') is False
        # Swap #main repeatedly: no duplicate SDK load, controller or listeners.
        await page.evaluate('''() => {
          document.querySelector('#main').innerHTML='<p>Another directory</p>';
          for(let i=0;i<4;i++) document.dispatchEvent(new Event('htmx:afterSwap'));
        }''')
        assert state['sdk'] == 1
        assert await page.locator('#cast-controller').count() == 1
        await page.get_by_role('button', name='Next', exact=True).click()
        await expect(page.locator('[data-cast-status]')).to_contain_text('2.jpg')
        assert await page.evaluate('fakeCast.loads.length') == 3
        await page.set_viewport_size({'width': 390, 'height': 844})
        await expect(page.get_by_role('button', name='Next', exact=True)).to_be_in_viewport()
        await page.evaluate('fakeCast.holdEnd = true')
        await page.get_by_role('button', name='Stop', exact=True).click()
        await expect(page.locator('[data-cast-status]')).to_contain_text('stopped')
        await page.wait_for_function('() => fakeCast.ended.length === 1')
        assert await page.evaluate('fakeCast.ended[0].stopCasting') is True
        assert await page.evaluate('fakeCast.current !== null') is True
        await page.evaluate('''() => {
          document.querySelector('#main').innerHTML='<button data-cast="/directories/test/cast" data-path="movie.mp4" data-mode="video">Cast video</button>';
          document.dispatchEvent(new Event('htmx:afterSwap'));
        }''')
        await expect(page.get_by_role('button', name='Cast video')).to_be_disabled()
        await page.evaluate('fakeCast.finishEnd();fakeCast.holdEnd=false')
        await expect(page.get_by_role('button', name='Cast video')).to_be_enabled()
        assert await page.evaluate('fakeCast.current') is None
        assert state['remove'] == 1
        assert len(requests) == 3
        await page.evaluate('''() => {
          document.querySelector('#main').innerHTML='<button data-cast="/directories/test/cast" data-path="movie.mp4" data-mode="video">Cast video</button>';
          document.dispatchEvent(new Event('htmx:afterSwap'));
        }''')
        await page.get_by_role('button', name='Cast video').click()
        await page.evaluate('fakeCast.choose()')
        await expect(page.locator('[data-cast-status]')).to_contain_text('movie.mp4')
        loaded = await page.evaluate('fakeCast.loads.at(-1)')
        assert loaded['media']['streamType'] == 'BUFFERED'
        assert loaded['media']['metadata']['metadataType'] == 1
        await page.evaluate('''() => {Object.assign(fakeCast.player,{duration:100,currentTime:25,isPaused:true,canSeek:true,isMediaLoaded:true,playerState:'PLAYING'});fakeCast.remoteChanged();}''')
        await page.get_by_role('button', name='Play', exact=True).click()
        assert await page.evaluate('fakeCast.toggled') is True
        await page.locator('[data-cast-seek]').fill('50')
        await page.locator('[data-cast-seek]').dispatch_event('change')
        assert await page.evaluate('fakeCast.sought') is True
        await page.locator('[data-cast-volume]').fill('0.5')
        await page.locator('[data-cast-volume]').dispatch_event('change')
        assert await page.evaluate('fakeCast.volume') is True
        await page.evaluate('''() => {fakeCast.player.playerState='IDLE';fakeCast.player.isMediaLoaded=false;fakeCast.remoteChanged();}''')
        await expect(page.locator('[data-cast-status]')).to_contain_text('Video ended')
        await page.evaluate('''() => {
          document.querySelector('#main').innerHTML='<button data-cast="/directories/test/cast" data-path="" data-mode="slideshow">Cast slideshow</button>';
          document.dispatchEvent(new Event('htmx:afterSwap'));
        }''')
        await page.get_by_role('button', name='Cast slideshow').click()
        await page.evaluate('fakeCast.choose()')
        await expect(page.locator('[data-cast-status]')).to_contain_text('0.jpg')
        timer = await page.evaluate('filesCast.operation.timer')
        await page.get_by_role('button', name='Previous', exact=True).click()
        assert await page.evaluate('filesCast.operation.timer') == timer
        pickers = await page.evaluate('fakeCast.pickers')
        session = await page.evaluate('fakeCast.current.getSessionId()')
        loads = await page.evaluate('fakeCast.loads.length')
        await page.get_by_role('button', name='Cast slideshow').click()
        await page.wait_for_function('(n) => fakeCast.loads.length === n + 1', arg=loads)
        assert await page.evaluate('fakeCast.pickers') == pickers
        assert await page.evaluate('fakeCast.current.getSessionId()') == session
        removed = state['remove']
        loads = await page.evaluate('fakeCast.loads.length')
        ended = await page.evaluate('fakeCast.ended.length')
        await page.evaluate('''() => {
          const button=document.querySelector('[data-cast]');
          button.dataset.cast='/s/existing/cast';button.dataset.path='empty';
        }''')
        await page.get_by_role('button', name='Cast slideshow').click()
        await expect(page.locator('[data-cast-status]')).to_contain_text('no JPEG or PNG')
        await page.wait_for_function('(n) => fakeCast.ended.length === n + 1 && !fakeCast.current', arg=ended)
        assert await page.evaluate('fakeCast.loads.length') == loads
        assert state['remove'] == removed + 1, 'Only the replaced owner share should be revoked'
        created = state['create']
        ended = await page.evaluate('fakeCast.ended.length')
        await page.get_by_role('button', name='Cast slideshow').click()
        await page.get_by_role('button', name='Stop', exact=True).click()
        await page.evaluate('fakeCast.choose()')
        await page.wait_for_function('(n) => fakeCast.ended.length === n + 1 && !fakeCast.current', arg=ended)
        assert state['create'] == created, 'Late picker selection created a share'
        await page.get_by_role('button', name='Cast slideshow').click()
        await page.evaluate('fakeCast.fail()')
        await expect(page.locator('[data-cast-status]')).to_contain_text('Unable to connect')
        # Session teardown must cover idle receivers and preserve unrelated media/sessions.
        for scope in ('idle', 'foreign-media', 'foreign-session'):
            await page.evaluate('''() => {
              const button=document.querySelector('[data-cast]');
              button.dataset.cast='/directories/test/cast';button.dataset.path='';
            }''')
            await page.get_by_role('button', name='Cast slideshow').click()
            await page.evaluate('fakeCast.choose()')
            await expect(page.locator('[data-cast-status]')).to_contain_text('0.jpg')
            ended = await page.evaluate('fakeCast.ended.length')
            await page.evaluate('''scope => {
              if (scope === 'idle') fakeCast.current.getMediaSession().media=null;
              else if (scope === 'foreign-media') fakeCast.current.getMediaSession().media={contentId:'https://unrelated.example/video.mp4'};
              else fakeCast.current=newSession();
            }''', scope)
            await page.get_by_role('button', name='Stop', exact=True).click()
            await expect(page.locator('[data-cast-status]')).to_contain_text('stopped')
            if scope == 'idle':
                await page.wait_for_function('(n) => fakeCast.ended.length === n + 1 && !fakeCast.current', arg=ended)
            else:
                assert await page.evaluate('fakeCast.ended.length') == ended
                assert await page.evaluate('fakeCast.current !== null') is True
                await page.evaluate('''() => {
                  fakeCast.current=null;fakeCast.sessionState='SESSION_ENDED';
                  fakeCast.emit('session',{sessionState:'SESSION_ENDED'});
                }''')
        assert not errors, errors
        await browser.close()
        print('PASS SDK session stop/restart, connected-session replacement, late selection, controls, HTMX, and empty public folders')

asyncio.run(main())
