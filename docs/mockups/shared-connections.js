// Sample path calculations only; connection forms use authored HTMX fixtures.
(() => {
  const userID = '8c32a7f1-4b6e-49d0-a2f5-1e97c8b603ad';
  function updatePaths() {
    const shared = document.getElementById('shared-connection');
    const prefix = document.getElementById('user-id-prefix');
    const options = document.querySelector('[data-user-prefix]');
    if (options) {
      options.hidden = !shared.checked;
      prefix.disabled = !shared.checked || prefix.dataset.prefixLocked === 'true';
      document.querySelector('[data-connection-path]').textContent =
        `s3://cloud-bucket/${prefix.checked ? userID + '/' : ''}projects/`;
      document.querySelector('[data-prefix-effect]').textContent = prefix.checked
        ? 'Each user gets a separate storage path.'
        : 'With this off, users can map the same objects.';
    }
    const path = document.querySelector('[data-mapping-path]');
    if (path) {
      const bucket = document.getElementById('mapping-bucket').value.trim();
      const mappingPrefix = document.getElementById('mapping-prefix').value.trim().replace(/^\/+|\/+$/g, '');
      const userPrefix = path.dataset.prefixEnabled === 'true' ? userID + '/' : '';
      path.textContent = `s3://${bucket || 'bucket'}/${userPrefix}${mappingPrefix ? mappingPrefix + '/' : ''}`;
    }
  }
  document.addEventListener('htmx:afterSwap', updatePaths);
  document.addEventListener('change', updatePaths);
  document.addEventListener('input', updatePaths);
})();
