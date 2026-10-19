// Copy the visible prompt so the displayed and copied instructions stay in sync.
document.querySelectorAll('button.copy').forEach(function (button) {
  var original = button.innerHTML;
  var reset;
  button.addEventListener('click', async function () {
    var target = button.dataset.copyTarget;
    var text = target ? document.getElementById(target).textContent : button.dataset.copy;
    var status = button.parentElement.querySelector('.copy-status');
    var copied = false;
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
    } catch (_) {
      var field = document.createElement('textarea');
      field.value = text;
      field.setAttribute('readonly', '');
      field.setAttribute('aria-label', 'Text to copy');
      document.body.appendChild(field);
      field.select();
      try { copied = document.execCommand('copy'); } catch (_) {}
      field.remove();
      button.focus();
    }
    clearTimeout(reset);
    button.textContent = copied ? 'Copied!' : 'Try copying again';
    if (status) status.textContent = copied ? 'Ready to paste into your agent.' : 'Copy unavailable. Select and copy the prompt above.';
    reset = setTimeout(function () {
      button.innerHTML = original;
      if (status) status.textContent = '';
    }, 3000);
  });
});
