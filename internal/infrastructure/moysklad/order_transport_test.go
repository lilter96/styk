package moysklad

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrderTransportALPNMatchesHTTP1Implementation(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprint(http2), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 1 {
					t.Errorf("order transport negotiated HTTP%d", r.ProtoMajor)
				}
				w.WriteHeader(200)
			}))
			server.EnableHTTP2 = http2
			server.StartTLS()
			defer server.Close()
			base, ok := server.Client().Transport.(*http.Transport)
			if !ok {
				t.Fatal("TLS transport unavailable")
			}
			inherited := base.Clone()
			inherited.TLSClientConfig = base.TLSClientConfig.Clone()
			// Simulate DefaultTransport initialized by an earlier HTTP/2 read.
			inherited.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
			inherited.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			client, closeHTTP := orderHTTP(&http.Client{Transport: inherited})
			defer closeHTTP()
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("HTTP1 order transport failed", err)
			}
			if _, err = io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			if err = response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.ProtoMajor != 1 {
				t.Fatal("HTTP implementation and negotiated protocol disagree")
			}
		})
	}
}
