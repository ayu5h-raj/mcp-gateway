package clientcfg

// Cursor's mcp.json shares the exact schema as Claude Desktop's mcpServers
// block. Reuse the parser; if the schema diverges in future, split here.
func readCursor(path string) ([]Server, error) {
	return readClaudeDesktop(path)
}

func patchCursor(path string, removedServers []string, gatewayBinary string) error {
	return patchClaudeDesktop(path, removedServers, gatewayBinary)
}
