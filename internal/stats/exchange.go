package stats

// RecordExchange accounts for the complete upstream attempt sequence, including
// a refresh unable to select any healthy endpoint. It never adds client traffic.
func (c *Collector) RecordExchange(attempts, probes int, refresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Version++
	if attempts > 0 {
		c.s.Attempts += uint64(attempts)
	}
	if attempts > 1 {
		c.s.Retries += uint64(attempts - 1)
	}
	if probes > 0 {
		c.s.HealthProbes += uint64(probes)
	}
	if refresh {
		c.s.BackgroundRefreshes++
	}
}
