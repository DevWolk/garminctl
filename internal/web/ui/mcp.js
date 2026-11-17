(() => {
  let loading = false, endpoint = '';
  const client = $('#mcp-client');
  client.innerHTML = window.mcpClients.map(({id, name}) => `<option value="${esc(id)}">${esc(name)}</option>`).join('');
  client.value = prefs.get('mcpClient', client.value);
  // The poll calls this every few seconds: an unchanged field keeps the user's selection.
  const showSetup = () => {
    if (!endpoint) return;
    const setup = window.mcpSetup(client.value, endpoint), field = $('#mcp-setup');
    $('#mcp-setup-label').textContent = setup.label;
    $('#mcp-setup-copy').textContent = setup.action;
    if (field.value === setup.text) return;
    field.value = setup.text;
    field.rows = setup.text.split('\n').length;
  };
  client.onchange = () => { prefs.set('mcpClient', client.value); showSetup(); };
  const when = (value) => value ? new Date(value).toLocaleString() : '–';
  window.loadMCP = async () => {
    if (loading) return;
    loading = true;
    try {
      const data = await api('GET', '/api/mcp');
      $('#mcp-endpoint').value = data.endpoint;
      endpoint = data.endpoint;
      showSetup();
      const clients = data.status.clients || [], calls = data.status.activity || [];
      const active = clients.filter(client => client.sessionActive);
      $('#mcp-status').textContent = `MCP available · ${active.length} open session${active.length === 1 ? '' : 's'}`;
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
        input.focus(); input.select(); toast('Copy the selected text');
      }
    };
  });
  setInterval(() => { if (state.tab === 'ai' && !document.hidden) window.loadMCP(); }, 3000);
  if (state.tab === 'ai') window.loadMCP();
})();
