// Local design fixtures only. Preview uses a bundled image; downloads show a receipt.
(() => {
  const dialog = document.querySelector('#sample-preview');
  let opener;
  document.addEventListener('click', (event) => {
    const preview = event.target.closest('[data-preview]');
    if (preview) {
      event.preventDefault();
      opener = preview;
      document.querySelector('#preview-title').textContent = preview.dataset.preview;
      dialog.showModal();
    }
    if (event.target.closest('[data-close-preview]')) dialog.close();
  });
  dialog.addEventListener('close', () => { if (opener?.isConnected) opener.focus(); });
  const themeLink = document.querySelector('[data-theme-link]');
  document.addEventListener('htmx:afterSwap', (event) => {
    if (event.detail.target.id !== 'shared-listing') return;
    const current = document.querySelector('[aria-current="page"]:not(.preview-bar a)');
    const folder = current?.textContent.toLowerCase();
    const query = folder === 'assets' || folder === 'exports' ? `?folder=${folder}` : '';
    history.replaceState(null, '', location.pathname + query);
    themeLink.href = themeLink.href.split('?')[0] + query;
  });
  const folder = new URLSearchParams(location.search).get('folder');
  if (document.querySelector('#shared-listing') && ['assets', 'exports'].includes(folder)) {
    htmx.ajax('GET', `fragments/public-${folder}.html`, {target: '#shared-listing', swap: 'innerHTML'});
  }
})();
