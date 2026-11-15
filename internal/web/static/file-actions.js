"use strict";
// Accessible tooltips are positioned within the viewport, including modal top layers.
(() => {
  const tooltip = document.createElement('div');
  tooltip.id = 'file-action-tooltip';
  tooltip.role = 'tooltip';
  tooltip.hidden = true;
  document.body.append(tooltip);
  let owner;
  const hide = () => { tooltip.hidden = true; owner?.removeAttribute('aria-describedby'); owner = null; };
  const show = target => {
    const control = target.closest?.('.icon-action[data-tooltip]');
    if (!control) return;
    hide(); owner = control;
    (control.closest('dialog') || document.body).append(tooltip);
    tooltip.textContent = control.dataset.tooltip; tooltip.hidden = false;
    control.setAttribute('aria-describedby', tooltip.id);
    const rect = control.getBoundingClientRect();
    tooltip.style.left = `${Math.max(8, Math.min(innerWidth - tooltip.offsetWidth - 8, rect.left + rect.width / 2 - tooltip.offsetWidth / 2))}px`;
    tooltip.style.top = `${Math.max(8, rect.top - tooltip.offsetHeight - 8)}px`;
  };
  ['pointerover', 'focusin'].forEach(name => document.addEventListener(name, event => show(event.target)));
  ['pointerout', 'focusout', 'click', 'htmx:beforeSwap'].forEach(name => document.addEventListener(name, hide, true));
  document.addEventListener('scroll', () => { if (owner === document.activeElement) show(owner); else hide(); }, true);
  window.addEventListener('resize', hide);
  document.addEventListener('keydown', event => { if (event.key === 'Escape') hide(); });
})();

// HTMX loads real forms and folder branches. Failed writes update only feedback,
// retaining names, selection and the expanded tree. No client-side storage state.
(() => {
  const dialog = document.getElementById('file-action-dialog');
  if (!dialog) return;
  const content = document.getElementById('file-action-dialog-content');
  let opener, requested = false;
  const action = () => document.querySelector('[data-file-action]');
  const label = (root, path) => root + (path ? ' / ' + path.split('/').join(' / ') : '');
  function close() {
    requested = false;
    if (opener) htmx.trigger(opener, 'htmx:abort');
    content.querySelectorAll('form,[data-folders-url]').forEach(el => htmx.trigger(el, 'htmx:abort'));
    dialog.close(); content.replaceChildren();
    if (opener?.isConnected) opener.focus();
  }
  function selection() {
    const scope = action();
    if (!scope || scope.dataset.fileAction !== 'move') return;
    const selected = scope.querySelector('[name=destination]:checked');
    if (!selected) return;
    const destination = selected.value;
    const full = label(scope.dataset.rootName, destination);
    scope.querySelector('[data-selected-path]').textContent = full;
    scope.querySelector('[data-create-parent-label]').textContent = full;
    scope.querySelector('[data-create-parent]').value = destination;
    const current = destination === scope.dataset.sourceParent;
    scope.querySelector('#file-move-submit').disabled = current;
    scope.querySelector('[data-move-hint]').textContent = current ? 'This file is already in this folder.' : 'The file will keep its name.';
  }
  function load(details) {
    const children = details.querySelector(':scope > [data-folder-children]');
    if (children.dataset.loaded || children.dataset.loading) return;
    children.dataset.loading = 'true';
    children.textContent = 'Loading folders…';
    htmx.ajax('GET', details.dataset.foldersUrl, {target: children, swap:'innerHTML', source: details});
  }
  function initialize() {
    const scope = action();
    if (!scope) return;
    if (scope.dataset.fileAction === 'move') {
      const selected = scope.querySelector('[name=destination]:checked')?.value ?? scope.dataset.sourceParent;
      const radio = [...scope.querySelectorAll('.folder-tree [name=destination]')].find(el => el.value === selected);
      if (radio) radio.checked = true;
      scope.querySelectorAll('[data-folder-path]').forEach(details => {
        const path = details.dataset.folderPath;
        if (path === '' || selected === path || selected.startsWith(path + '/')) { details.open = true; load(details); }
      });
      selection();
    }
  }
  document.addEventListener('htmx:beforeRequest', event => {
    if (event.detail.target === content) { opener = event.detail.elt; requested = true; }
  });
  document.addEventListener('htmx:afterRequest', event => {
    const children = event.detail.requestConfig?.target;
    if (!children?.matches('[data-folder-children]')) return;
    delete children.dataset.loading;
    if (event.detail.xhr.status >= 200 && event.detail.xhr.status < 300) children.dataset.loaded = 'true';
    else { children.textContent = 'Could not load folders. Collapse and expand this folder to retry.'; }
  });
  document.addEventListener('htmx:afterSwap', event => {
    const target = event.detail.target;
    if (target === content && requested) {
      // Bind the new forms before exposing them to fast keyboard submission;
      // HTMX's normal settle phase otherwise runs shortly after afterSwap.
      htmx.process(content);
      const scope = action();
      document.getElementById('file-action-title').textContent = scope?.dataset.fileAction === 'move' ? 'Move file' : scope ? 'Rename file' : 'File action';
      document.getElementById('file-action-description').textContent = scope?.dataset.name || '';
      if (!dialog.open) dialog.showModal();
      const name = content.querySelector('#file-rename-input');
      if (name) { name.focus(); const dot = name.value.lastIndexOf('.'); name.setSelectionRange(0, dot > 0 ? dot : name.value.length); }
      else content.querySelector('.folder-tree input:checked, .folder-tree input')?.focus();
    }
    if (target.id === 'folder-create-result') {
      const created = target.querySelector('[data-created-folder]');
      const node = created?.querySelector('template')?.content.firstElementChild.cloneNode(true);
      if (node) {
        const scope = action();
        const parent = scope.querySelector('[data-create-parent]').value;
        const parentNode = [...scope.querySelectorAll('.folder-node')].find(el => el.dataset.nodePath === parent);
        const details = parentNode.querySelector(':scope > details');
        const children = details.querySelector(':scope > [data-folder-children]');
        // Cancel any older branch listing so it cannot replace the new folder.
        htmx.trigger(details, 'htmx:abort');
        let list = children.querySelector(':scope > ul');
        if (!list) { list = document.createElement('ul'); children.replaceChildren(list); }
        list.querySelectorAll(':scope > .empty-tree').forEach(el => el.remove());
        const existing = [...list.children].find(el => el.dataset.nodePath === node.dataset.nodePath);
        if (existing) existing.remove();
        list.append(node); children.dataset.loaded = 'true'; details.open = true;
        const empty = node.querySelector('[data-folder-children]');
        empty.dataset.loaded = 'true';
        const message = document.createElement('p'); message.className = 'empty-tree'; message.textContent = 'No subfolders'; empty.append(message);
        node.querySelector('input').checked = true;
        htmx.process(node);
        scope.querySelector('#move-create-folder').open = false;
        scope.querySelector('#move-folder-name').value = '';
        scope.querySelector('#folder-create-feedback').replaceChildren();
        node.scrollIntoView({block:'nearest'}); node.querySelector('input').focus();
      }
    }
    if (['main','app-layout'].includes(target.id)) {
      if (dialog.open) {
        close();
        const result = document.getElementById('file-action-result');
        const focus = result?.textContent.trim() ? result : document.querySelector('.browse-heading h1');
        if (focus) { focus.tabIndex = -1; focus.focus(); }
      }
    }
    if (target === content || target.matches('[data-folder-children]') || ['folder-create-result','main','app-layout'].includes(target.id)) initialize();
    if (['file-action-feedback','folder-create-feedback'].includes(target.id)) target.focus();
  });
  document.addEventListener('toggle', event => {
    if (event.target.matches('[data-folders-url]') && event.target.open) load(event.target);
  }, true);
  document.addEventListener('change', event => { if (event.target.matches('.folder-tree [name=destination]')) selection(); });
  document.addEventListener('click', event => {
    if (event.target.closest('[data-close-file-action]') || (dialog.open && event.target.closest('[data-cancel-file-action]'))) { event.preventDefault(); close(); }
    const summary = event.target.closest('[data-delete-action]');
    if (!summary) return;
    event.preventDefault(); opener = summary; requested = false;
    const form = summary.parentElement.querySelector('form').cloneNode(true);
    form.setAttribute('hx-target', '#file-action-feedback');
    form.setAttribute('data-file-action-form', '');
    const footer = document.createElement('div'); footer.className = 'file-form-actions';
    const cancel = document.createElement('button'); cancel.type = 'button'; cancel.className = 'secondary outline'; cancel.dataset.closeFileAction = ''; cancel.textContent = 'Cancel';
    footer.append(cancel, form.querySelector('button')); form.append(footer);
    const feedback = document.createElement('div'); feedback.id = 'file-action-feedback'; feedback.tabIndex = -1; feedback.setAttribute('aria-live','polite');
    content.replaceChildren(feedback, form); htmx.process(form);
    document.getElementById('file-action-title').textContent = summary.getAttribute('aria-label') + '?';
    document.getElementById('file-action-description').textContent = summary.dataset.name;
    dialog.showModal(); form.querySelector('input[type=checkbox]').focus();
  });
  dialog.addEventListener('cancel', event => { event.preventDefault(); close(); });
  document.addEventListener('htmx:sendError', event => {
    if (event.detail.target === content) return;
    if (!event.detail.elt.closest('[data-file-action-form]') && !event.detail.elt.matches('[data-folders-url]')) return;
    const feedback = document.getElementById(event.detail.target.id === 'folder-create-feedback' ? 'folder-create-feedback' : 'file-action-feedback');
    if (feedback) { feedback.textContent = 'The request could not be completed. Your entries are still here; check your connection and retry.'; feedback.focus(); }
  });
  document.addEventListener('DOMContentLoaded', initialize);
})();
