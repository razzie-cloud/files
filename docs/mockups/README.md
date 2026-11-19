# Files design concept

## Shares overview

[Light](shares.html) · [Dark](dark/shares.html) ·
[Empty](shares-empty.html) · [Dark empty](dark/shares-empty.html) ·
[Create share](share-create.html) · [Dark creation](dark/share-create.html)

**Shares** is available in every authenticated preview’s sidebar. The overview
shows links across Workspace, Photo archive and Personal vault, with source
directory, file/folder scope, password requirement and Active/Expired status.
Active links open a recipient preview in a new tab; expired links remain visible
for cleanup. Revoke replaces only its card with an authored simulation receipt;
no real capability is changed. Empty state and Browse directories explain how
to create a link. Existing file/folder Share actions still open the separate
creation fixture, which links back to the overview. All mutations are local
static GET fixtures with no submitted input values.

The production service implements the overview at `/shares`, with owner-scoped
metadata and real revocation. These previews remain independent sample data.

Screenshots: [overview desktop](shares-desktop.png),
[overview mobile](shares-mobile.png), [dark overview](dark/shares-desktop.png),
[dark mobile](dark/shares-mobile.png), [empty](shares-empty-desktop.png),
[dark empty mobile](dark/shares-empty-mobile.png), and
[creation](share-create-desktop.png). Each sharing screen has desktop/mobile
images in both themes; affected primary sidebar screenshots were refreshed.

Verification: Chromium checked 112 full-page layouts in both themes at
320/390/768/1440 pixels. Checks covered headings, assets, page overflow, sidebar
links, selected state, account/header and card/badge alignment, theme round trips,
simulated revocation, creation and input privacy. All 1008 observed requests were
local GETs with no submitted values or failed responses; no unexpected browser
or CSP errors occurred. Representative desktop/mobile screenshots were visually
reviewed. All 892 local HTML references resolve in their document context.

Production verification passed `gofmt`, `go build ./...`, `go vet ./...`,
`go test ./...` and `go test -race ./...` in Files. The maintained Shares browser
fixture uses real handlers with disposable owner-scoped metadata and checks
populated/empty/creation states in both themes at four widths, real revoke POSTs,
direct links and fresh Back/Forward restoration. The existing panel-navigation
browser regression also passed. PostgreSQL integration tests skipped because
`TEST_DATABASE_URL` was unset; in-memory S3 tests ran. Production screenshots
were kept separate from these mockups. No live S3/authentication, publication or
deployment ran; temporary browser fixtures exited after verification.

## Shared recipient layout proposal

[Single file](public-file.html) · [Dark single file](dark/public-file.html) ·
[Folder](public.html) · [Dark folder](dark/public.html)

The recipient shell shows **Read-only** once, as a quiet lock label beside
**Shared with you**. Header, table caption and footer no longer repeat access
restrictions. A single file uses one card with its filename, format, size and
labeled Preview/Download controls; the one-row table and duplicate filename are
removed. Folder shares retain a compact table and show item counts beneath the
folder title. The public shell has no account or management controls.

Filename and folder links inherit the table's neutral text color in both themes.
The color follow-up passed eight layouts at 320/390/768/1440 pixels, including
focused links and names loaded through HTMX; the four folder screenshots were
refreshed and visually reviewed. Browser/CSP/console and whitespace checks passed.

The preview bar switches between file/folder examples and light/dark themes.
Assets opens a sample nested folder; Exports shows an empty folder. Breadcrumbs
return to the share root, and theme links preserve the current folder. Preview
opens a native dialog with a bundled sample image; Close/Escape returns focus.
Download fetches an authored HTMX receipt. These fixtures do not download the
original GIF or persist changes. Production now implements the reviewed layout
with real share routes and the existing preview, Cast and pagination behavior;
these mockups remain independent sample fixtures.

Screenshots: [dark proposal](dark/public-file-proposal.png),
[light proposal](public-file-proposal.png),
[dark mobile file](dark/public-file-mobile.png),
[light mobile file](public-file-mobile.png),
[dark folder](dark/public-desktop.png),
[light folder](public-desktop.png),
[dark mobile folder](dark/public-mobile.png),
[light mobile folder](public-mobile.png), and
[mobile preview](dark/public-preview-mobile.png).

Verification: headless Brave/Chromium rendered the four full pages at
320/390/768/1440 pixels (16 layout cases). Checks covered one heading and one
access label, loaded images, page overflow, file/folder/theme links, folder
breadcrumbs, empty state, deep links, preview/Close/Escape/focus return, keyboard
activation, download receipts and button/icon/context alignment. Twelve saved
screenshots were visually inspected. Local references and Git whitespace were
checked. No unexpected browser/CSP/console errors or failed responses occurred;
all observed page requests were local GETs. The temporary browser/server exited
after verification. No Go service checks or live S3/authentication acceptance ran
for this static-only proposal.


Saved S3 provider settings are called **connections** throughout these previews.
The primary connection list and first-connection desktop screenshots were
refreshed in both themes; other screenshots record earlier design reviews.

## File actions, rename and move

Open the [interactive light preview](file-actions.html) or
[dark preview](dark/file-actions.html). Run `go run scripts/serve.go` from the
parent checkout and visit
<http://127.0.0.1:8765/files/docs/mockups/file-actions.html>.

The file browser replaces repeated action labels with line icons. Hover or focus
an icon for its tooltip; each control also has an accessible name. On narrow
screens the table becomes stacked rows and icons wrap. Open, Download, Share,
Cast and confirmed deletion remain represented alongside the new file actions.
Existing sharing/Cast/folder controls open explanatory placeholders here.

- **Rename** opens a native dialog and selects the filename stem, retaining the
  visible, editable extension. The preview updates the sample row after saving.
- **Move** opens a collapsible folder hierarchy scoped to **Workspace**. The root
  itself and any child folder can be destinations. The current folder starts
  selected, with Move disabled until another destination is chosen. Other root
  directories are absent; moving across roots is unsupported.
- **Create subfolder here** adds a child of the selected destination and selects
  it for the move. Folder creation can be repeated to build nested destinations.
  The selected destination's full path stays visible below the tree.
- Conflict fixtures retain the name/destination draft and offer no overwrite.
  Invalid names and duplicate sample folders are blocked. Cancel, Close and
  Escape return focus; removing a row moves focus to its result message.
- **Read-only preview** hides writes and retains opening, downloads and sharing.
  Theme navigation preserves that state. Folder renames/moves are outside this
  concept; folder rows retain Share and Remove empty folder.

Changes exist only in browser memory and reset on reload. HTMX fetches authored
static success/conflict/folder fragments with `hx-params="none"`; names and file
selections never enter requests. These are mockups, not implemented storage
operations. No backend, production templates or production assets were changed.

Review screenshots (matching dark images live in `dark/`):
[browser](file-actions-desktop.png), [move picker](file-move-desktop.png),
[create destination](file-move-create-desktop.png), [rename](file-rename-desktop.png),
[mobile browser](file-actions-mobile.png), [mobile move](file-move-mobile.png),
and [mobile folder creation](file-move-create-mobile.png).

Verification: headless Chromium checked light/dark and writable/read-only at
320, 390, 768 and 1440 pixels. Rename/save, invalid names, retained conflict
drafts, Cancel/Close/Escape, focus return, keyboard tooltips, tree expand/collapse,
root destinations, subfolder creation, duplicate folders, move/delete results,
and theme round trips passed. No observed browser/CSP errors, failed responses,
external requests, writes or transmitted drafts occurred. Desktop/mobile
screenshots were visually reviewed. Static checks covered all five new
pages/fragments and their 57 local references, unique IDs and external scripts/
styles; parent and component whitespace checks passed. No service Go checks or
live S3/auth/Cast acceptance ran for this mockup-only change.

The folder-creation sizing follow-up overrides Pico's full-width dialog submit
button on desktop: the name field expands beside a content-sized button.
Mobile explicitly retains stacked full-width controls. Chromium verified control
proportions/alignment, overflow and folder creation in both themes at all four
widths; the four affected screenshots were refreshed and visually reviewed.

Production Files now implements the reviewed file actions, Rename/Move dialogs,
lazy folder branches and inline destination-folder creation. These static
previews remain separate fixtures. See the
[live behavior and limits](../reference.md#renaming-and-moving-files) and the
parent [implementation verification](../../../docs/frontend-mockups.md#files-rename-and-move-concept).

Share now uses a balanced three-node symbol; Cast uses a screen outline with a
clear cutout around the broadcast waves. The production templates, both themes,
sharing forms and detail fragments use matching inline SVG geometry. The
affected desktop/mobile screenshots were refreshed. Chromium checked 24 changed
mockup layouts and 24 rendered production layouts at 320/390/768/1440 pixels,
including tooltips and Share/Cast fixture actions. No unexpected browser/CSP
errors or failed responses occurred. Files build, vet, tests and race tests
passed; PostgreSQL integration skipped with `TEST_DATABASE_URL` unset for this
icon-only follow-up. No production storage or Cast hardware was used.

## Connection bucket controls

Every connection has **Use connection**, including personal sample cards. New
connection forms include **Fixed bucket (optional)** and default path-style
addressing off. The shared Cloud connection sample fixes `cloud-bucket`; its mapping
preview shows a read-only bucket. Team archive and Personal connection keep the
bucket editable. Existing edit fixtures retain their saved addressing choice.

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
`go run scripts/serve.go`, then open
<http://127.0.0.1:8765/files/docs/mockups/index.html>.

| Screen | File | Review focus |
| --- | --- | --- |
| Directory overview | [index.html](index.html) | Directory cards, shared shell, connections |
| Nested file browser | [browse.html](browse.html) | Pico breadcrumbs, file table, contextual details and uploads |
| Intermediate folders | [workspace.html](workspace.html), [projects.html](projects.html), [folder.html](folder.html) | Clickable ancestor navigation and empty folder layout |
| Connections | [connections.html](connections.html) | Connection cards with Add/Edit dialogs |
| First connection | [connections-empty.html](connections-empty.html) | Inline setup when no connections exist |
| Add directory | [directory.html](directory.html) | Grouped setup, encryption disclosure, explanatory column |
| Shares | [shares.html](shares.html), [empty](shares-empty.html) | Links across directories, expiry status and simulated revocation |
| Create share | [share-create.html](share-create.html) | Read-only capability link form |
| Recipient view | [public-file.html](public-file.html), [public.html](public.html) | Single-file card and folder table with one access label |

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
