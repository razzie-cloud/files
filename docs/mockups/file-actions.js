// Sample-data behavior only. HTMX fetches authored fragments; drafts stay local.
(() => {
  const $ = (selector) => document.querySelector(selector);
  const dialog = $('#file-dialog');
  const sourcePath = 'Projects/Brand refresh';
  const pathLabel = (path) => `Workspace${path ? ` / ${path.split('/').join(' / ')}` : ''}`;
  let opener, row, operation, pendingName, pendingDestination, pendingParent;
  let destination = sourcePath;
  const readonly = new URLSearchParams(location.search).get('access') === 'readonly';
  const tooltip = document.createElement('div');
  tooltip.id = 'action-tooltip';
  tooltip.role = 'tooltip';
  tooltip.hidden = true;
  document.body.append(tooltip);
  let tooltipOwner;
  const hideTooltip = () => {
    tooltip.hidden = true;
    tooltipOwner?.removeAttribute('aria-describedby');
    tooltipOwner = null;
  };
  const showTooltip = (event) => {
    const button = event.target.closest('.icon-action');
    if (!button) return;
    hideTooltip();
    tooltipOwner = button;
    (button.closest('dialog') || document.body).append(tooltip);
    tooltip.textContent = button.dataset.tooltip;
    tooltip.hidden = false;
    button.setAttribute('aria-describedby', tooltip.id);
    const rect = button.getBoundingClientRect();
    tooltip.style.left = `${Math.max(8, Math.min(innerWidth - tooltip.offsetWidth - 8, rect.left + rect.width / 2 - tooltip.offsetWidth / 2))}px`;
    tooltip.style.top = `${Math.max(8, rect.top - tooltip.offsetHeight - 8)}px`;
  };
  document.addEventListener('pointerover', showTooltip);
  document.addEventListener('focusin', showTooltip);
  ['pointerout', 'focusout', 'click'].forEach((name) => document.addEventListener(name, hideTooltip, true));
  document.addEventListener('scroll', () => {
    if (tooltipOwner === document.activeElement) showTooltip({target: tooltipOwner});
    else hideTooltip();
  }, true);
  window.addEventListener('resize', hideTooltip);
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape') hideTooltip(); });
  if (readonly) {
    document.querySelectorAll('[data-write], [data-demo="folder-delete"]').forEach((el) => el.remove());
    $('#access-badge').textContent = $('#access-detail').textContent = 'Read-only';
    $('#access-badge').classList.remove('green');
    $('#root-note').textContent = 'This directory is read-only. Opening, downloads and sharing remain available.';
    $('[data-access-link]').href = 'file-actions.html';
    $('[data-access-link]').textContent = 'Writable preview';
    $('[data-theme-link]').href += '?access=readonly';
  }
  const close = () => {
    hideTooltip();
    dialog.close();
    if (opener?.isConnected) opener.focus();
    else { $('#action-result').tabIndex = -1; $('#action-result').focus(); }
  };
  const selectDestination = () => {
    destination = $('.folder-tree input:checked').value;
    $('#selected-path').textContent = $('#create-parent').textContent = pathLabel(destination);
    $('#move-submit').disabled = destination === sourcePath;
    $('#move-hint').textContent = destination === sourcePath ? 'This file is already in this folder.' : 'The file will keep its name.';
    $('#move-error').replaceChildren();
    $('#new-folder-result').replaceChildren();
  };
  function open(panel, title, description) {
    document.querySelectorAll('[data-panel]').forEach((el) => { el.hidden = el.dataset.panel !== panel; });
    $('#dialog-title').textContent = title;
    $('#dialog-description').textContent = description;
    $('#dialog-eyebrow').textContent = panel === 'move' ? 'Within Workspace' : 'File action';
    dialog.showModal();
  }
  document.addEventListener('click', (event) => {
    const button = event.target.closest('button');
    if (!button) return;
    if (button.hasAttribute('data-close')) { close(); return; }
    if (button.dataset.scroll) { $( `#${button.dataset.scroll}`).scrollIntoView({behavior: 'smooth'}); $('#upload-files input').focus({preventScroll:true}); return; }
    if (!button.dataset.action && !button.dataset.demo) return;
    opener = button;
    row = button.closest('[data-file-row]');
    operation = button.dataset.action;
    const name = row?.dataset.name || 'Brand refresh';
    if (operation === 'rename') {
      $('#rename-error').replaceChildren();
      $('#rename-input').value = name;
      open('rename', 'Rename file', `${pathLabel(sourcePath)} / ${name}`);
      $('#rename-input').focus();
      const dot = name.lastIndexOf('.');
      $('#rename-input').setSelectionRange(0, dot > 0 ? dot : name.length);
    } else if (operation === 'move') {
      $('#create-folder').open = false;
      $('#folder-input').value = '';
      document.querySelectorAll('.folder-tree details').forEach((el) => { el.open = true; });
      $(`.folder-tree input[value="${sourcePath}"]`).checked = true;
      selectDestination();
      open('move', 'Move file', name);
      $('.folder-tree input:checked').focus();
    } else if (operation === 'delete') {
      $('#confirm-delete').checked = false;
      document.querySelectorAll('[data-selected-name]').forEach((el) => { el.textContent = name; });
      open('delete', 'Delete file?', name);
      $('[data-panel="delete"] [data-close]').focus();
    } else if (operation === 'open') {
      document.querySelectorAll('[data-selected-name]').forEach((el) => { el.textContent = name; });
      open('open', 'File preview', name);
    } else if (operation === 'download') {
      $('#action-result').textContent = `Download preview for ${name}. No storage request was made.`;
    } else {
      const demo = button.dataset.demo;
      const messages = {
        share: ['Share', 'The existing sharing screen would open here. This concept focuses on file actions.'],
        cast: ['Cast', 'The Cast control would connect to an available receiver. This preview does not connect to a device.'],
        'folder-delete': ['Remove empty folder', 'The existing confirmed empty-folder removal would open here. Folder moves and renames are outside this concept.'],
        'new-folder': ['New folder', 'Use Move on a file to try creating a subfolder inside the destination picker. The existing standalone folder form would open here.']
      };
      const [title, message] = messages[demo];
      $('#demo-message').textContent = message;
      open('demo', title, name);
    }
  });
  $('.folder-tree').addEventListener('change', selectDestination);
  dialog.addEventListener('cancel', (event) => { event.preventDefault(); close(); });
  function validName(input, errorTarget) {
    const name = input.value.trim();
    const error = !name || name === '.' || name === '..' || /[\\/]/.test(name);
    input.setCustomValidity(error ? 'Enter a name without slashes.' : '');
    if (error) { input.reportValidity(); return false; }
    if (errorTarget) $(errorTarget).replaceChildren();
    return true;
  }
  ['#rename-input', '#folder-input'].forEach((id) => $(id).addEventListener('input', () => $(id).setCustomValidity('')));
  // Guard before HTMX handles the submit, including blank/invalid folder names.
  document.addEventListener('submit', (event) => {
    const form = event.target;
    let allowed = true;
    if (form.id === 'rename-form') {
      allowed = validName($('#rename-input'), '#rename-error');
      pendingName = $('#rename-input').value.trim();
      if (pendingName === row.dataset.name) {
        $('#rename-input').setCustomValidity('Choose a different name.');
        $('#rename-input').reportValidity(); allowed = false;
      }
    } else if (form.id === 'create-folder-form') {
      allowed = validName($('#folder-input'));
      pendingName = $('#folder-input').value.trim();
      pendingParent = destination;
      const newPath = `${destination ? `${destination}/` : ''}${pendingName}`;
      if ([...document.querySelectorAll('.folder-tree input')].some((el) => el.value === newPath)) {
        $('#folder-input').setCustomValidity('A folder with this name already exists here.');
        $('#folder-input').reportValidity(); allowed = false;
      }
    } else if (form.id === 'move-form') {
      allowed = destination !== sourcePath;
      pendingDestination = destination;
    }
    if (!allowed) { event.preventDefault(); event.stopImmediatePropagation(); }
  }, true);
  document.addEventListener('htmx:afterSwap', (event) => {
    if (event.detail.target.id === 'new-folder-result' && $('#created-folder-template')) {
      const node = $('#created-folder-template').content.firstElementChild.cloneNode(true);
      const newPath = `${pendingParent ? `${pendingParent}/` : ''}${pendingName}`;
      node.querySelector('input').value = newPath;
      node.querySelector('.folder-choice>span').textContent = pendingName;
      node.querySelector('[data-children]').dataset.children = newPath;
      [...document.querySelectorAll('.folder-tree [data-children]')].find((el) => el.dataset.children === pendingParent).append(node);
      node.querySelector('input').checked = true;
      selectDestination();
      $('#new-folder-result').textContent = `Created “${pendingName}” in this preview. Selected as the destination.`;
      $('#create-folder').open = false;
      node.scrollIntoView({block:'nearest'});
      node.querySelector('input').focus();
    }
    if (event.detail.target.id === 'action-result' && $('[data-saved]')) {
      let feedback = 'Upload simulated. No files were transmitted.';
      if (dialog.open && row) {
        const oldName = row.dataset.name;
        if (operation === 'rename') {
          row.dataset.name = pendingName;
          row.querySelector('.filename').textContent = pendingName;
          feedback = `Renamed “${oldName}” to “${pendingName}” in this preview.`;
        } else if (operation === 'move') {
          row.remove();
          feedback = `Moved “${oldName}” to ${pathLabel(pendingDestination)} in this preview.`;
        } else if (operation === 'delete') {
          row.remove();
          feedback = `Deleted “${oldName}” in this preview.`;
        }
        const count = document.querySelectorAll('[data-file-row]').length;
        $('#file-count').textContent = `${count} files`;
        $('#item-count').textContent = `${count + 2} items`;
        close();
      }
      $('[data-saved]').textContent = `${feedback} No storage was changed.`;
    }
  });
})();
