# Files design concept

## Shared S3 connection concept

New standalone previews: [administrator](shared-connections-admin.html),
[regular user](shared-connections-member.html),
[dark administrator](dark/shared-connections-admin.html) and
[dark regular user](dark/shared-connections-member.html).
Production now implements the shared connection controls and path behavior.
These pages remain independent static review fixtures.

- **Add connection** starts personal. Administrators see **Shared**, off by
  default. Enabling it reveals **Add user ID as a prefix**, also off by default.
- Edit shows a disabled Shared switch for administrators, preserving the
  creation-time choice. Personal connections never show the user-prefix option.
  Regular users can manage their personal connections and use shared ones;
  shared credentials and editing controls are absent from their view.
- Shared cards show prefix-on and prefix-off examples. **Use connection** opens
  a path preview with bucket and directory prefix. With the option on, the path
  is `s3://<bucket>/<gateway-user-uuid>/<directory-prefix>/`. Without it, users
  mapping the same location can access the same S3 objects. Directory settings
  remain personal. Encryption and other directory options use existing setup.
- The prefix option locks while any user has a directory attached. The sample
  shared edits show this fixed state; unused shared connections can change it.
  No S3 objects are moved or deleted.
- Files and Notes manage independent connections; sharing means all users of
  the service, not synchronizing credentials between the two services.

Cancel/Escape/Close return focus. Error fixtures retain drafts, and saves return
fixed sample cards for the current role. No input values are sent or saved;
every form fetches a static fragment with `hx-params="none"`. Access controls
here are visual fixtures, not live authorization.

Screenshots: [admin list](shared-connections-admin-desktop.png),
[user list](shared-connections-member-desktop.png),
[add dialog](shared-connection-add-desktop.png),
[edit dialog](shared-connection-edit-desktop.png),
[path preview](shared-connection-path-desktop.png) and
[mobile add](shared-connection-add-mobile.png). Matching images live in `dark/`.

Serve the **Razzie Cloud parent directory** with
`python3 -m http.server 8765 --bind 127.0.0.1`, then open
<http://127.0.0.1:8765/files/docs/mockups/index.html>.

| Screen | File | Review focus |
| --- | --- | --- |
| Directory overview | [index.html](index.html) | Directory cards, shared shell, storage connections |
| Nested file browser | [browse.html](browse.html) | Pico breadcrumbs, file table, contextual details and uploads |
| Intermediate folders | [workspace.html](workspace.html), [projects.html](projects.html), [folder.html](folder.html) | Clickable ancestor navigation and empty folder layout |
| Storage connections | [connections.html](connections.html) | Connection cards with Add/Edit dialogs |
| First connection | [connections-empty.html](connections-empty.html) | Inline setup when no connections exist |
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

## Connection dialog concept

Existing connections show cards and **Add connection**, with no inline connection
form. Add and Edit fetch static form fragments through HTMX into an HTML5
`dialog`, opened with `showModal()`. Edit fills the selected connection's public
settings; credential inputs remain blank. Empty setup keeps the first form inline.
Cancel, Close and Escape discard the draft and return focus to the opener.
Save closes the dialog and shows explicit fixture feedback with the sample cards.
The preview does not persist entered values. **Preview a save error** keeps the
draft in place. All connection fixture forms use `hx-params="none"` and unnamed
inputs so credentials never enter requests.

Both themes include existing and empty states. Screenshots:
[list](connections-desktop.png), [edit dialog](connections-edit-desktop.png),
[mobile dialog](connections-edit-mobile.png), [empty](connections-empty-desktop.png),
and their counterparts under `dark/`. The small external
`storage-connections.js` handles native dialog lifecycle; HTMX handles fixture
requests. The reviewed flow is now implemented in the production templates with
real save routes, revision checks and draft-preserving error feedback. These
mockup fixtures still simulate all mutations.

The connection concept passed Chromium checks in both themes at 320, 390, 768
and 1440 pixels for existing/empty states (16 layout cases). Checks exercised
Add/Edit, native modal focus, Escape/Close/Cancel, settings prefill, blank
credentials, validation, error draft retention, simulated saves, bucket tests,
removal feedback and theme round trips. Network inspection confirmed no entered
values were transmitted; no browser errors or failed responses were observed.
Local references, unique IDs, synchronized modal assets and Git whitespace were
checked. Screenshots were inspected visually. No Go checks or live backend
acceptance were run for these mockup-only changes.

See [the coordinated design notes](../../../docs/frontend-mockups.md) in the
parent checkout for scope and verification details.
