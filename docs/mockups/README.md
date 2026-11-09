# Files design concept

Serve the **Razzie Cloud parent directory** with
`python3 -m http.server 8765 --bind 127.0.0.1`, then open
<http://127.0.0.1:8765/files/docs/mockups/index.html>.

| Screen | File | Review focus |
| --- | --- | --- |
| Directory overview | [index.html](index.html) | Directory cards, shared shell, storage connections |
| Nested file browser | [browse.html](browse.html) | Pico breadcrumbs, file table, contextual details and uploads |
| Intermediate folders | [workspace.html](workspace.html), [projects.html](projects.html), [folder.html](folder.html) | Clickable ancestor navigation and empty folder layout |
| Storage connections | [connections.html](connections.html) | Form beside existing provider cards |
| Add directory | [directory.html](directory.html) | Grouped setup, encryption disclosure, explanatory column |
| Sharing | [shares.html](shares.html) | Read-only capability link form |
| Recipient view | [public.html](public.html) | Simplified public shell |

[Overview screenshot](index-desktop.png) · [File browser screenshot](browse-desktop.png)

## Dark-mode screenshots

Open [the dark preview](dark/index.html), or use **Dark mode** in any preview's
top bar. **Light mode** returns to the same screen. Dark pages use the same PicoCSS
and HTMX assets, with navy surfaces and gold actions in `dark.css`.

| Screen | Screenshot |
| --- | --- |
| Directory overview | [Dark image](dark/index-desktop.png) |
| File browser | [Dark image](dark/browse-desktop.png) |
| Connections | [Dark image](dark/connections-desktop.png) |
| Add directory | [Dark image](dark/directory-desktop.png) |
| Sharing | [Dark image](dark/shares-desktop.png) |
| Recipient view | [Dark image](dark/public-desktop.png) |
| Workspace | [Dark image](dark/workspace-desktop.png) |
| Projects | [Dark image](dark/projects-desktop.png) |
| Empty folder | [Dark image](dark/folder-desktop.png) |

Try a file's **Details**, the **New folder** disclosure, the upload form, share
creation, or switching directory encryption. Actions display sample responses.
Never enter real storage credentials. Read-only/encrypted directory cards are
visual variants; all Open directory links lead to the same writable sample.

Assets are referenced directly from `../../internal/web/static/`. `mockup.css`
adds layout and Pico variable overrides. `fragments/` contains HTMX fixtures.
Production templates now implement this layout and automatically follow the
browser's light/dark preference, retaining advanced settings and live file actions.
These mockup pages remain separate fixtures with simulated interactions.

See [the coordinated design notes](../../../docs/frontend-mockups.md) in the
parent checkout for scope and verification details.
