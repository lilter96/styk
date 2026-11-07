package providercall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"
)

type readObserverKey struct{}

// ReadEvidence is opt-in diagnostic material. It deliberately excludes headers,
// credentials and URLs with query parameters. Its consumer must protect Body.
type ReadEvidence struct {
	At                          time.Time
	Scope                       Scope
	Method, Path, RequestDigest string
	Status                      int
	Body                        []byte
	Failure                     error
}

// WithReadObserver records actual coordinated reads without replacing a trusted
// HTTP transport. It cannot observe or authorize a provider mutation.
func WithReadObserver(ctx context.Context, observer func(ReadEvidence) error) context.Context {
	return context.WithValue(ctx, readObserverKey{}, observer)
}

func observeProviderRead(req *http.Request, scope Scope, response *http.Response, networkErr error) error {
	observer, ok := req.Context().Value(readObserverKey{}).(func(ReadEvidence) error)
	if !ok || observer == nil || isMutation(req) {
		return nil
	}
	evidence := ReadEvidence{At: time.Now().UTC(), Scope: scope, Method: req.Method, Path: req.URL.Path, Failure: networkErr}
	requestErr := readRequestDigest(req, &evidence)
	evidence.Failure = errors.Join(networkErr, requestErr)
	if response != nil {
		evidence.Status = response.StatusCode
		body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		closeErr := response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		evidence.Body, evidence.Failure = body, errors.Join(networkErr, requestErr, err, closeErr)
		if len(body) > 4<<20 {
			evidence.Failure = errors.Join(evidence.Failure, errors.New("provider read evidence exceeds response limit"))
		}
	}
	return errors.Join(observer(evidence), evidence.Failure)
}

func readRequestDigest(req *http.Request, evidence *ReadEvidence) error {
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, body)
	closeErr := body.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	evidence.RequestDigest = hex.EncodeToString(hash.Sum(nil))
	return nil
}
