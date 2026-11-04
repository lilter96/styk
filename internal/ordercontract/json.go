package ordercontract

// CheckJSON reuses the bounded contract parser, including duplicate-key and
// trailing-value rejection, for typed provider DTOs outside the legacy manifest.
func CheckJSON(data []byte) error {
	_, err := decodePayload(data)
	return err
}
