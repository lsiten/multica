package daemon

// mcpBroker reads the built-in MCP owner seam under a read lock so a runtime
// handover (an in-process bridge being retired for a recovered gateway process)
// is never observed as a torn or nil pointer by a concurrent task.
func (d *Daemon) mcpBroker() mcpBroker {
	d.mcpBrokerMu.RLock()
	defer d.mcpBrokerMu.RUnlock()
	return d.builtinMCP
}

// setMCPBroker swaps the built-in MCP owner seam under a write lock. It is the
// only writer, so a task that captured a broker before the swap keeps using the
// old (still-open) owner and a task that reads after the swap gets the new one.
func (d *Daemon) setMCPBroker(b mcpBroker) {
	d.mcpBrokerMu.Lock()
	defer d.mcpBrokerMu.Unlock()
	d.builtinMCP = b
}
