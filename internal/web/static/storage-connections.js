"use strict";
// The dialog lives outside replaceable page content. HTMX owns form loading and
// saves; native dialog APIs own modal focus and Escape behavior.
(() => {
  const dialog = document.getElementById("connection-dialog");
  if (!dialog) return;
  const content = document.getElementById("connection-dialog-content");
  let opener;
  function closeDialog() {
    dialog.close();
    // Clear drafts now, rather than in the queued native close event, which can
    // arrive after a subsequent Add/Edit request has loaded its form.
    content.replaceChildren();
    if (opener?.isConnected) opener.focus();
    else document.getElementById("add-connection")?.focus();
  }
  document.addEventListener("htmx:beforeRequest", event => {
    if (event.detail.target === content) opener = event.detail.elt;
  });
  document.addEventListener("htmx:beforeSwap", event => {
    if (event.detail.xhr.status >= 400 && ["connection-feedback", "connection-list-feedback"].includes(event.detail.target.id)) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });
  document.addEventListener("htmx:afterSwap", event => {
    const target = event.detail.target;
    if (target === content && content.querySelector("[data-connection-form]") && !dialog.open) {
      dialog.showModal();
      // Avoid autofocus attributes: HTMX's later settle phase would refocus the
      // name field while the user is already entering other settings.
      content.querySelector("[name=name]").focus();
    }
    if (["main", "app-layout"].includes(target.id)) {
      if (dialog.open) closeDialog();
      document.getElementById("add-connection")?.focus();
    }
    if (["connection-feedback", "connection-list-feedback"].includes(target.id)) {
      target.focus();
      target.scrollIntoView({ block: "nearest" });
    }
  });
  document.addEventListener("click", event => {
    if (event.target.closest("[data-close-connection]")) closeDialog();
  });
  dialog.addEventListener("cancel", event => {
    event.preventDefault();
    closeDialog();
  });
  document.addEventListener("htmx:sendError", event => {
    const form = event.detail.elt.closest("[data-connection-form]");
    if (!form) return;
    const feedback = form.querySelector("#connection-feedback");
    feedback.textContent = "Connection could not be saved. Your entries are still here; check your connection and retry.";
    feedback.focus();
    feedback.scrollIntoView({ block: "nearest" });
  });
})();

// Sharing fields and physical path previews work for inline and HTMX forms.
(() => {
  function refresh() {
    document.querySelectorAll('[data-connection-form]').forEach(form => {
      const shared = form.querySelector('[name=shared]');
      const options = form.querySelector('[data-shared-options]');
      if (!options || !shared) return;
      options.hidden = !shared.checked;
      const prefix = options.querySelector('[type=checkbox]');
      if (prefix && prefix.dataset.prefixLocked !== 'true') prefix.disabled = !shared.checked;
    });
    document.querySelectorAll('[data-effective-path]').forEach(output => {
      const form = output.closest('form');
      const option = form.querySelector('[name=connection_id]')?.selectedOptions[0];
      const bucketField = form.querySelector('[name=bucket]');
      const fixed = option?.dataset.fixedBucket || '';
      if (fixed) {
        if (bucketField.dataset.bucketFixed !== 'true') bucketField.dataset.freeBucket = bucketField.value;
        bucketField.value = fixed;
        bucketField.dataset.bucketFixed = 'true';
      } else if (bucketField.dataset.bucketFixed === 'true') {
        bucketField.value = bucketField.dataset.freeBucket || '';
        delete bucketField.dataset.bucketFixed;
      }
      bucketField.readOnly = Boolean(fixed);
      const help = form.querySelector('[data-connection-bucket-help]');
      if (help) help.hidden = !fixed;
      if (!option?.value) { output.textContent = ''; return; }
      const bucket = bucketField.value.trim() || 'bucket';
      const prefix = form.querySelector('[name=prefix]').value.trim().replace(/^\/+|\/+$/g, '');
      const userPrefix = option.dataset.userIdPrefix === 'true' ? output.dataset.owner + '/' : '';
      const suffix = option.dataset.shared === 'true' && !userPrefix ? ' Other users mapping this location access the same objects.' : '';
      output.textContent = 'Storage path: s3://' + bucket + '/' + userPrefix + (prefix ? prefix + '/' : '') + suffix;
    });
  }
  document.addEventListener('DOMContentLoaded', refresh);
  document.addEventListener('htmx:afterSwap', refresh);
  document.addEventListener('change', refresh);
  document.addEventListener('input', refresh);
})();
