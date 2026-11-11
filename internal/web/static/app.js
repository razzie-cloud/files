"use strict";
function configureCrypt() {
  document.querySelectorAll("[data-encryption]").forEach(select => {
    const options = select.closest("form").querySelector("[data-crypt]");
    options.hidden = select.value !== "rclone_crypt";
  });
}
document.addEventListener("change", event => { if (event.target.matches("[data-encryption]")) configureCrypt(); });
document.addEventListener("htmx:afterSwap", configureCrypt);
document.addEventListener("DOMContentLoaded", configureCrypt);
document.addEventListener("htmx:beforeSwap", event => {
  if (event.detail.xhr.status >= 400) { event.detail.shouldSwap = true; event.detail.isError = false; }
});
document.addEventListener("htmx:xhr:progress", event => {
  const progress = event.target.closest("[data-upload]")?.querySelector("progress");
  if (progress && event.detail.lengthComputable) { progress.hidden = false; progress.value = 100 * event.detail.loaded / event.detail.total; }
});
document.addEventListener("click", event => {
  const cancel = event.target.closest("[data-cancel-upload]");
  if (cancel) htmx.trigger(cancel.closest("form"), "htmx:abort");
  if (event.target.closest("[data-back]")) { event.preventDefault(); history.back(); }
});
// Cross-origin auth redirects cannot be followed by an HTMX XHR. Repeat as a
// top-level navigation to let the gateway establish a fresh app session.
document.addEventListener("htmx:sendError", event => {
  if (event.detail.elt.closest("[data-connection-form]")) return;
  location.assign(location.pathname + location.search);
});

// Let the rest of a file row activate its name link, keeping actions independent.
document.addEventListener("click", event => {
  if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  if (event.target.closest("a, button, input, select, textarea, label, details, form, [contenteditable]")) return;
  if (window.getSelection()?.isCollapsed === false) return;
  const link = event.target.closest(".file-table tbody tr")?.querySelector("[data-entry-open]");
  if (link) {
    event.preventDefault();
    link.click();
  }
});

// Native dialogs preserve the folder, trap focus, and support Escape to close.
let previewLinks = [];
let previewIndex = 0;
function clearPreview(dialog) {
  const image = dialog.querySelector("[data-preview-image]");
  image.hidden = true;
  image.removeAttribute("src");
  dialog.querySelectorAll("[data-preview-video], [data-preview-audio]").forEach(media => {
    media.pause();
    media.hidden = true;
    media.removeAttribute("src");
    media.load(); // Release the previous stream, including when the dialog closes.
  });
  dialog.querySelectorAll("[data-preview-document], [data-preview-pdf]").forEach(frame => {
    frame.hidden = true;
    frame.removeAttribute("src");
  });
}
function showPreview(dialog, index) {
  clearPreview(dialog);
  previewIndex = index;
  const link = previewLinks[index];
  dialog.querySelector("#media-title").textContent = link.dataset.name;
  dialog.querySelector("[data-preview-error]").hidden = true;
  const download = new URL(link.href);
  download.searchParams.delete("inline");
  dialog.querySelector("[data-preview-download]").href = download.href;
  dialog.querySelector("[data-preview-tab]").href = link.href;
  dialog.querySelector("[data-preview-previous]").disabled = index === 0;
  dialog.querySelector("[data-preview-next]").disabled = index === previewLinks.length - 1;
  dialog.querySelector("[data-preview-position]").textContent = `${index + 1} of ${previewLinks.length}`;
  const media = dialog.querySelector(`[data-preview-${link.dataset.preview}]`);
  if (media.tagName === "IMG") media.alt = link.dataset.name;
  if (media.tagName === "IFRAME") media.title = link.dataset.name;
  media.hidden = false;
  media.src = link.href;
}
document.addEventListener("click", event => {
  const link = event.target.closest("[data-preview]");
  const dialog = document.querySelector("#media-preview");
  if (!dialog) return;
  if (link) {
    if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || !dialog.showModal) return;
    event.preventDefault();
    // A file has both a name link and an Open action; visit it only once.
    previewLinks = Array.from(new Map(Array.from(document.querySelectorAll("[data-preview]"), item => [item.href, item])).values());
    showPreview(dialog, previewLinks.findIndex(item => item.href === link.href));
    dialog.showModal();
  } else if (event.target.closest("[data-preview-close]") || event.target === dialog) {
    dialog.close();
  } else if (event.target.closest("[data-preview-previous]") && previewIndex > 0) {
    showPreview(dialog, previewIndex - 1);
  } else if (event.target.closest("[data-preview-next]") && previewIndex < previewLinks.length - 1) {
    showPreview(dialog, previewIndex + 1);
  }
});
document.addEventListener("close", event => {
  if (event.target.matches("#media-preview")) {
    clearPreview(event.target);
    previewLinks = [];
  }
}, true);
document.addEventListener("error", event => {
  if (event.target.matches("[data-preview-image], [data-preview-video], [data-preview-audio]") && !event.target.hidden && event.target.hasAttribute("src")) {
    event.target.closest("dialog").querySelector("[data-preview-error]").hidden = false;
  }
}, true);
document.addEventListener("htmx:beforeCleanupElement", event => {
  const dialog = event.detail.elt.querySelector?.("#media-preview");
  if (dialog) {
    dialog.close();
    clearPreview(dialog);
  }
});


// Orient navigation within long pages and after server-rendered HTMX updates.
(() => {
  let highlighted, timeout;
  function hashTarget() {
    try {
      const target = document.getElementById(decodeURIComponent(location.hash.slice(1)));
      return target?.matches("[data-navigation-target]") ? target : null;
    } catch { return null; }
  }
  function highlight(target) {
    clearTimeout(timeout);
    highlighted?.classList.remove("navigation-highlight");
    highlighted = null;
    if (!target) return;
    // Restart the cue when the same navigation link is activated again.
    void target.offsetWidth;
    target.classList.add("navigation-highlight");
    highlighted = target;
    timeout = setTimeout(() => {
      target.classList.remove("navigation-highlight");
      highlighted = null;
    }, 1500);
  }
  document.addEventListener("DOMContentLoaded", () => {
    highlight(hashTarget());
  });
  window.addEventListener("hashchange", () => highlight(hashTarget()));
  document.addEventListener("click", event => {
    const link = event.target.closest("a[href]");
    if (!link || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || (link.target && link.target !== "_self")) return;
    if (link.origin === location.origin && link.pathname === location.pathname && link.search === location.search && link.hash && link.hash === location.hash) highlight(hashTarget());
  });
  document.addEventListener("htmx:afterSettle", event => {
    const updated = event.detail.target || event.detail.elt;
    if (!(updated instanceof Element)) return;
    const target = hashTarget();
    if (!target || !updated.contains(target)) return;
    // A fragment target may not exist until the activity panel has loaded.
    target.scrollIntoView({ block: "start", behavior: "auto" });
    highlight(target);
  });
})();
