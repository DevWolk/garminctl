'use strict';
// One <dialog id="dlg"> serves every confirmation and prompt. A request made
// while another is open cancels the open one first, so an answer only reaches
// the request it was shown for.
(() => {
  const dlg = document.getElementById('dlg');
  let shown = null; // { resolve, result } of the request on screen

  dlg.addEventListener('close', () => {
    // A close queued before the dialog was reopened for a newer request is stale.
    if (dlg.open || !shown) return;
    const { resolve, result } = shown;
    shown = null;
    const value = dlg.returnValue;
    // Esc and light dismiss set no value; clear it so the next request starts empty.
    dlg.returnValue = '';
    resolve(result(value));
  });

  function showDialog(fill, result) {
    if (shown) {
      const replaced = shown;
      shown = null;
      replaced.resolve(replaced.result(''));
    }
    // Reopen so focus starts on Cancel again instead of the replaced dialog's button.
    if (dlg.open) dlg.close();
    return new Promise((resolve) => {
      shown = { resolve, result };
      fill();
      dlg.returnValue = '';
      dlg.showModal();
    });
  }

  function setText(title, ok) {
    document.getElementById('dlg-title').textContent = title;
    document.getElementById('dlg-ok').textContent = ok;
  }

  // Resolves true only when this request's OK button closed the dialog.
  window.confirmDialog = ({ title, body, ok }) => showDialog(() => {
    setText(title, ok || 'Confirm');
    document.getElementById('dlg-body').innerHTML = body;
  }, (value) => value === 'ok');

  // Resolves the trimmed text, or null when cancelled or dismissed.
  window.promptDialog = (title, label, value, ok) => {
    const input = document.createElement('input');
    input.type = 'text';
    input.value = value;
    input.style.width = '100%';
    input.style.marginTop = '6px';
    // Cancel is the form's default button, so Enter would cancel; it accepts instead.
    input.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      if (event.isComposing || event.keyCode === 229) return;
      dlg.close('ok');
    });
    const field = document.createElement('label');
    field.className = 'form';
    field.style.display = 'block';
    field.append(label, input);
    const answer = showDialog(() => {
      setText(title, ok || 'OK');
      document.getElementById('dlg-body').replaceChildren(field);
    }, (result) => (result === 'ok' ? input.value.trim() : null));
    input.select();
    return answer;
  };
})();
