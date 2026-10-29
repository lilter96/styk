package orders

func (registry MetadataRegistry) Validate() error {
	if registry.Scope.Validate() != nil || registry.Revision < 0 || registry.ObservedAt.IsZero() {
		return ErrInvalid
	}
	if registry.CreationIntentID != "" && !validID(registry.CreationIntentID) {
		return ErrInvalid
	}
	codes := make(map[string]bool, len(registry.Attributes))
	ids := make(map[string]bool, len(registry.Attributes))
	for _, attribute := range registry.Attributes {
		if !validID(attribute.UniqueCode) || !validUUID(attribute.AttributeID) || !metadataTextType(attribute.Type) {
			return ErrInvalid
		}
		if codes[attribute.UniqueCode] || ids[attribute.AttributeID] {
			return ErrConflict
		}
		codes[attribute.UniqueCode], ids[attribute.AttributeID] = true, true
	}
	return nil
}

func metadataTextType(kind string) bool {
	return kind == "string" || kind == "text"
}
