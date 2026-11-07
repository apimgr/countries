document.addEventListener('click', async (event) => {
  const button = event.target.closest('[data-copy-code]');
  if (!button) return;
  const code = button.closest('.code-block')?.querySelector('pre')?.textContent;
  if (!code || !navigator.clipboard) {
    button.textContent = 'Copy unavailable';
    return;
  }
  try {
    await navigator.clipboard.writeText(code);
    button.textContent = 'Copied';
    window.setTimeout(() => { button.textContent = 'Copy'; }, 2000);
  } catch {
    button.textContent = 'Copy failed';
  }
});
