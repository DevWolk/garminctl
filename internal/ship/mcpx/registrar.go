package mcpx

import "github.com/modelcontextprotocol/go-sdk/mcp"

// ToolRegistrar mounts feature tools on a newly constructed protocol server.
type ToolRegistrar interface {
	RegisterTools(*mcp.Server)
}
