package labelstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

func TestEncryptedLabelRoundTripAndUnknownPut(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		ambiguous, unavailable bool
	}{{name: "success"}, {name: "lost put acknowledgement", ambiguous: true}, {name: "unavailable storage", unavailable: true}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var stored []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if tc.unavailable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if r.Method == http.MethodPut {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					stored = data
					if tc.ambiguous {
						w.WriteHeader(500)
					}
					return
				}
				if _, err := w.Write(stored); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			store := testStore(server)
			protector, artifact, pdf := testArtifact(t)
			external, err := store.StoreOrderLabel(context.Background(), artifact)
			if tc.unavailable {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("expected unavailable, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(external.Ciphertext, pdf) || bytes.Equal(external.Ciphertext, artifact.Ciphertext) {
				t.Fatal("database reference contains label bytes")
			}
			loaded, err := store.LoadOrderLabel(context.Background(), external)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := protector.OpenOrderLabel(loaded)
			if err != nil || !bytes.Equal(plain, pdf) {
				t.Fatal("encrypted object round trip failed")
			}
			mu.Lock()
			stored = []byte("corrupted")
			mu.Unlock()
			if _, err = store.LoadOrderLabel(context.Background(), external); !errors.Is(err, ErrUnavailable) {
				t.Fatal("object tampering accepted")
			}
		})
	}
}

func testStore(server *httptest.Server) *Store {
	cfg := aws.Config{Region: "us-east-1", Credentials: awscredentials.NewStaticCredentialsProvider("test", "test", ""), HTTPClient: server.Client(), RetryMaxAttempts: 1}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })
	return &Store{client: client, bucket: "assets"}
}

func testArtifact(t *testing.T) (orderprivacy.LabelProtector, orders.LabelArtifact, []byte) {
	t.Helper()
	vault, err := credentials.New(bytes.Repeat([]byte{13}, 32))
	if err != nil {
		t.Fatal(err)
	}
	p := orderprivacy.LabelProtector{Vault: vault}
	pdf := []byte("%PDF-1.7\nPRIVATE LABEL\n%%EOF")
	a, err := p.SealOrderLabel(orders.Work{ID: "work-test", Scope: orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}}, orders.ActionTask{ID: "task-test", Type: "small_label"}, pdf, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return p, a, pdf
}

func TestReferenceCannotSelectOtherObjectOrExpiry(t *testing.T) {
	t.Parallel()
	_, a, _ := testArtifact(t)
	var absent *Store
	inline, err := absent.LoadOrderLabel(context.Background(), a)
	if err != nil || !bytes.Equal(inline.Ciphertext, a.Ciphertext) {
		t.Fatal("legacy inline artifact unreadable")
	}
	a.Ciphertext = []byte(`{"neon_label_object":"arbitrary/key"}`)
	if _, err = absent.LoadOrderLabel(context.Background(), a); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing store accepted reference")
	}
	key := "labels/" + "0/" + string(bytes.Repeat([]byte("a"), 64))
	if validKey(key, a.ExpiresAt) {
		t.Fatal("mismatched retention accepted")
	}
}
