"""Run with Python + Playwright/Chromium; no frontend build step.

Uses the real dialog and local assets with disposable in-browser HTTP fixtures.
"""
import asyncio
import io
import math
import os
from pathlib import Path
import struct
from urllib.parse import parse_qs, urlparse
import wave

from playwright.async_api import async_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
STATIC = ROOT / 'internal/web/static'


def audio_fixture():
    out = io.BytesIO()
    with wave.open(out, 'wb') as audio:
        audio.setnchannels(1)
        audio.setsampwidth(2)
        audio.setframerate(8000)
        audio.writeframes(b''.join(struct.pack('<h', int(1000 * math.sin(i / 8))) for i in range(32000)))
    return out.getvalue()


def page_html(public=False):
    templates = (ROOT / 'internal/web/templates.html').read_text()
    dialog = templates.split('{{define "media-dialog"}}')[1].split('{{end}}')[0]
    base = '/s/capability' if public else '/directories/test'
    links = ''.join(
        f'<a href="{base}/download?path={name}&amp;inline=1" data-preview="{kind}" data-name="{name}">Open {name}</a> '
        for name, kind in [('tone.wav', 'audio'), ('broken.mp3', 'audio'), ('photo.svg', 'image')]
    )
    return ('<!doctype html><html><head><meta charset="utf-8">'
            '<meta name="viewport" content="width=device-width,initial-scale=1">'
            '<link rel="stylesheet" href="/static/pico.min.css"><link rel="stylesheet" href="/static/app.css">'
            '<script defer src="/static/htmx.min.js"></script><script defer src="/static/app.js"></script>'
            '</head><body><main id="main">' + links + dialog + '</main></body></html>')


async def main():
    wav = audio_fixture()
    async with async_playwright() as p:
        options = {'args': ['--no-sandbox']}
        if executable := os.environ.get('PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH'):
            options['executable_path'] = executable
        browser = await p.chromium.launch(**options)
        page = await browser.new_page()
        page.set_default_timeout(5000)
        errors = []
        page.on('pageerror', lambda error: errors.append(str(error)))

        async def route(request_route):
            url = urlparse(request_route.request.url)
            if url.path.startswith('/static/'):
                await request_route.fulfill(path=STATIC / Path(url.path).name)
            elif url.path.endswith('/download'):
                name = parse_qs(url.query)['path'][0]
                if name == 'tone.wav':
                    await request_route.fulfill(body=wav, content_type='audio/wav', headers={'Accept-Ranges': 'bytes'})
                elif name == 'broken.mp3':
                    await request_route.fulfill(body=b'not audio', content_type='audio/mpeg')
                else:
                    await request_route.fulfill(body='<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"/>', content_type='image/svg+xml')
            else:
                await request_route.fulfill(body=page_html(url.path == '/shared'), content_type='text/html', headers={
                    'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'",
                })

        await page.route('https://files.test/**', route)
        for width in [1280, 390]:
            await page.set_viewport_size({'width': width, 'height': 844})
            for path in ['/private', '/shared']:
                await page.goto('https://files.test' + path)
                links = page.locator('[data-preview]')
                dialog = page.get_by_role('dialog')
                audio = page.locator('[data-preview-audio]')
                await links.first.click()
                await expect(audio).to_be_visible()
                await page.wait_for_function('() => document.querySelector("audio").readyState >= 2')
                assert await audio.evaluate('(a) => a.paused'), 'Audio must wait for Play'
                await audio.evaluate('(a) => a.play()')
                await page.wait_for_function('() => document.querySelector("audio").currentTime > 0')
                await audio.evaluate('(a) => { a.pause(); a.currentTime = 2; }')
                await page.wait_for_function('() => !document.querySelector("audio").seeking')
                assert abs(await audio.evaluate('(a) => a.currentTime') - 2) < .1
                await page.get_by_role('button', name='Next →', exact=True).click()
                await expect(page.locator('[data-preview-error]')).to_be_visible()
                await page.get_by_role('button', name='Next →', exact=True).click()
                await expect(audio).to_be_hidden()
                assert await audio.evaluate('(a) => a.paused && !a.hasAttribute("src")')
                assert 'inline=' not in await page.locator('[data-preview-download]').get_attribute('href')
                await page.keyboard.press('Escape')
                await expect(dialog).to_be_hidden()
                await links.first.click()
                await audio.evaluate('(a) => a.play()')
                await page.keyboard.press('Escape')
                await page.wait_for_function('() => { const a=document.querySelector("audio"); return a.paused && !a.hasAttribute("src"); }')
                assert await links.first.evaluate('(a) => a === document.activeElement')
                await links.first.click()
                await audio.evaluate('(a) => a.play()')
                await page.evaluate("async () => { await htmx.ajax('GET', '/fragment', {target:'#main', swap:'innerHTML'}); }")
                await expect(page.get_by_role('dialog')).to_be_hidden()
                await page.locator('[data-preview]').first.click()
                await expect(page.locator('[data-preview-audio]')).to_be_visible()
                await page.keyboard.press('Escape')
        assert not errors, errors
        await browser.close()
        print('PASS: audio playback, seeking, errors, navigation, cleanup, focus, HTMX; private/shared; desktop/mobile')


asyncio.run(main())
