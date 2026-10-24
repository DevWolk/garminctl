(() => {
  let loading = false;
  const when = (value) => value ? new Date(value).toLocaleString() : '–';
  window.loadMCP = async () => {
    if (loading) return;
    loading = true;
    try {
      const data = await api('GET', '/api/mcp');
      $('#mcp-endpoint').value = data.endpoint;
      $('#mcp-claude').value = `claude mcp add --transport http --scope user garminctl ${data.endpoint}`;
      const clients = data.status.clients || [], calls = data.status.activity || [];
      const active = clients.filter(client => client.sessionActive);
      $('#mcp-status').textContent = `MCP is available · ${active.length} open sessions`;
      $('#mcp-clients').innerHTML = active.length ? active.map(client => `<tr><th scope="row">${esc(client.name)}</th><td>${esc(client.version || '–')}</td><td>${esc(when(client.connectedAt))}</td><td>${esc(when(client.lastSeen))}</td></tr>`).join('') : '<tr><td colspan="4" class="muted">No open sessions. Connect an assistant using the command or URL above.</td></tr>';
      const recent = clients.filter(client => !client.sessionActive);
      $('#mcp-recent').innerHTML = recent.length ? recent.map(client => `<p>${esc(client.name)} ${esc(client.version)} · last seen ${esc(when(client.lastSeen))}</p>`).join('') : '<p>No closed sessions yet.</p>';
      $('#mcp-activity').innerHTML = calls.length ? calls.map(call => `<tr><td>${esc(when(call.startedAt))}</td><td>${esc(call.client)}</td><th scope="row">${esc(call.tool)}</th><td>${call.running ? 'Running' : call.failed ? 'Error' : 'Success'}</td><td>${call.running ? '–' : esc(call.durationMs + ' ms')}</td></tr>`).join('') : '<tr><td colspan="5" class="muted">No tool calls yet.</td></tr>';
    } catch (error) {
      $('#mcp-status').textContent = `MCP status unavailable: ${error.message}`;
    } finally { loading = false; }
  };
  $('#mcp-refresh').onclick = window.loadMCP;
  document.querySelectorAll('[data-mcp-copy]').forEach(button => {
    button.onclick = async () => {
      const input = document.getElementById(button.dataset.mcpCopy);
      if (!input.value) return;
      try {
        if (window.runtime?.ClipboardSetText) await window.runtime.ClipboardSetText(input.value);
        else await navigator.clipboard.writeText(input.value);
        toast('Copied');
      } catch {
        input.focus(); input.select(); toast('Press Ctrl+C to copy the selected text');
      }
    };
  });
  setInterval(() => { if (state.tab === 'ai' && !document.hidden) window.loadMCP(); }, 3000);
  if (state.tab === 'ai') window.loadMCP();
})();
