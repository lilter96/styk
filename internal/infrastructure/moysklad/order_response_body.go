package moysklad

import (
	"io"
	"net/http"
)

// Explicit Accept-Encoding disables net/http's implicit decompression. Bound
// both the wire representation and decoded JSON before identity verification.
func readOrderResponseBody(response *http.Response) ([]byte, error) {
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, orderResponseLimit+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, ErrOrderUnknown
	}
	return decompressResponse(response.Header, raw, orderResponseLimit)
}
