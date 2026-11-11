// Only modal lifecycle needs JavaScript. HTMX loads authored form/result fixtures.
(() => {
  const dialog = document.getElementById('connection-dialog');
  const content = document.getElementById('connection-dialog-content');
  let opener;
  function closeDialog() {
    dialog.close();
    // Clear drafts synchronously: native close events are queued and may arrive
    // after the next form request has already filled the dialog.
    content.replaceChildren();
    if (opener?.isConnected) opener.focus();
    else document.getElementById('add-connection')?.focus();
  }
  document.addEventListener('htmx:beforeRequest', (event) => {
    if (event.detail.target === content) opener = event.detail.elt;
  });
  document.addEventListener('htmx:afterSwap', (event) => {
    if (event.detail.target === content && content.childElementCount && !dialog.open) {
      dialog.showModal();
      content.querySelector('#connection-name')?.focus();
    }
    if (event.detail.target.id === 'connection-area') {
      if (dialog.open) closeDialog();
      document.getElementById('add-connection')?.focus();
    }
  });
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-close-connection]')) closeDialog();
  });
  dialog.addEventListener('cancel', (event) => {
    event.preventDefault();
    closeDialog();
  });
})();
