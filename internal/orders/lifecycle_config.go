package orders

func (config Config) validateLifecycle() error {
	policy := config.Lifecycle
	if policy == nil || !policy.Enabled {
		return nil
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if policy.Scope != config.Scope || policy.ConfigRevision != config.Revision || !config.Enabled || !config.TestEnabled || !config.ReadsEnabled() {
		return ErrInvalid
	}
	for _, mapping := range policy.Mappings {
		if mapping.StateID == config.StateID {
			return ErrInvalid
		}
		for _, forward := range config.StatusMappings {
			if forward.StateID == mapping.StateID {
				return ErrInvalid
			}
		}
	}
	return validateMerchantMappingCycles(policy.Mappings)
}

func validateMerchantMappingCycles(mappings []MerchantCommandMapping) error {
	graph := make(map[string][]string, len(mappings))
	for _, mapping := range mappings {
		for _, from := range mapping.FromStateIDs {
			key := mapping.Scheme + ":" + from
			graph[key] = append(graph[key], mapping.Scheme+":"+mapping.StateID)
		}
	}
	visited := make(map[string]uint8, len(graph))
	for node := range graph {
		if merchantMappingCycle(graph, visited, node) {
			return ErrInvalid
		}
	}
	return nil
}

func merchantMappingCycle(graph map[string][]string, visited map[string]uint8, node string) bool {
	if visited[node] == 1 {
		return true
	}
	if visited[node] == 2 {
		return false
	}
	visited[node] = 1
	for _, next := range graph[node] {
		if merchantMappingCycle(graph, visited, next) {
			return true
		}
	}
	visited[node] = 2
	return false
}
