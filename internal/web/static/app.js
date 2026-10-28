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
document.addEventListener("htmx:sendError", () => { location.assign(location.pathname + location.search); });
