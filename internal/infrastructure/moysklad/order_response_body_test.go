package moysklad

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func gzipOrderFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestNativeAcknowledgmentDecodesExplicitGzipBeforeIdentityChecks(t *testing.T) {
	t.Parallel()
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact account", true: "wrong account"}[foreign], func(t *testing.T) {
			t.Parallel()
			assertCompressedNativeAcknowledgment(t, foreign)
		})
	}
}

func assertCompressedNativeAcknowledgment(t *testing.T, foreign bool) {
	t.Helper()
	simulator, client := newOrderSimulator(t, "ok")
	command := simulator.command
	command.Action = orders.ActionDemandCreate
	command.NativeMovement = &orders.NativeMovement{Correlation: orders.NativeDocumentCorrelation{Kind: "demand", SyncID: command.Correlation.SyncID, Marker: command.Correlation.Marker}}
	data := simulator.reference("entity/demand", "demand", 9)
	data["id"], data["accountId"] = orderTestID(9), command.Scope.MoyskladAccount
	data["syncId"], data["externalCode"] = command.Correlation.SyncID, command.Correlation.Marker
	if foreign {
		data["accountId"] = "another-account"
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}}, Body: io.NopCloser(bytes.NewReader(gzipOrderFixture(t, raw)))}
	receipt, err := client.orderResponse(command, response)
	if foreign {
		if !errors.Is(err, ErrOrderUnknown) || receipt.State != orders.StateUnknown || receipt.ExternalID != "" {
			t.Fatal("compressed foreign identity was accepted")
		}
		return
	}
	if err != nil || receipt.State != orders.StateAcknowledged || receipt.HTTPStatus != 200 || receipt.ExternalID != orderTestID(9) {
		t.Fatalf("gzip acknowledgment not verified: %+v %v", receipt, err)
	}
}

func TestCompressedResponseEnforcesBothWireAndDecodedLimits(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "wire", true: "inflated"}[compressed], func(t *testing.T) {
			t.Parallel()
			body := []byte(strings.Repeat("x", 2048))
			header := make(http.Header)
			if compressed {
				body = gzipOrderFixture(t, body)
				header.Set("Content-Encoding", "gzip")
			}
			_, err := decompressResponse(header, body, 1024)
			var limit *LimitError
			if !errors.As(err, &limit) {
				t.Fatal("response limit was not enforced", err)
			}
		})
	}
}

func TestMalformedCompressedWriteResponseRemainsUnknown(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}}, Body: io.NopCloser(strings.NewReader("not a gzip response"))}
	receipt, err := client.orderResponse(simulator.command, response)
	if !errors.Is(err, ErrOrderUnknown) || receipt.State != orders.StateUnknown || receipt.ExternalID != "" || receipt.HTTPStatus != 200 {
		t.Fatal("malformed accepted response fabricated a successful identity")
	}
}
