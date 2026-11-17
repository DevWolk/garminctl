// Connection text per assistant, for the endpoint this server reports.
(() => {
  const command = 'run once in your terminal';
  const clients = [
    {id: 'claude', name: 'Claude Code', where: command, action: 'Copy command',
      text: url => `claude mcp add --transport http --scope user garminctl ${url}`},
    {id: 'codex', name: 'Codex CLI', where: command, action: 'Copy command',
      text: url => `codex mcp add garminctl --url ${url}`},
    {id: 'gemini', name: 'Gemini CLI', where: command, action: 'Copy command',
      text: url => `gemini mcp add --transport http --scope user garminctl ${url}`},
    {id: 'antigravity', name: 'Antigravity', where: 'merge into ~/.gemini/config/mcp_config.json', action: 'Copy configuration',
      text: url => JSON.stringify({mcpServers: {garminctl: {serverUrl: url}}}, null, 2)},
  ];
  window.mcpClients = clients.map(({id, name}) => ({id, name}));
  window.mcpSetup = (id, url) => {
    const client = clients.find(candidate => candidate.id === id) || clients[0];
    return {label: `${client.name} — ${client.where}`, action: client.action, text: client.text(url)};
  };
})();
