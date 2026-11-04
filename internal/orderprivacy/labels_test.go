package orderprivacy

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

func TestLabelEncryptionBindsEveryChunkAndOwner(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{53}, 32))
	if err != nil {
		t.Fatal(err)
	}
	protector := LabelProtector{Vault: vault}
	work := orders.Work{ID: "work", Scope: orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}}
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("PRIVATE_RECEIVER"), 12000)...)
	pdf = append(pdf, []byte("\n%%EOF")...)
	a, err := protector.SealOrderLabel(work, orders.ActionTask{ID: "1", Type: "big_label"}, pdf, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(a.Ciphertext, []byte("PRIVATE_RECEIVER")) {
		t.Fatal("plaintext stored")
	}
	actual, err := protector.OpenOrderLabel(a)
	if err != nil || !bytes.Equal(actual, pdf) {
		t.Fatal("PDF did not round-trip")
	}
	for _, change := range []func(*orders.LabelArtifact){
		func(a *orders.LabelArtifact) { a.Scope.Tenant = "other" }, func(a *orders.LabelArtifact) { a.Scope.MoyskladAccount = "other" }, func(a *orders.LabelArtifact) { a.WorkID = "other" }, func(a *orders.LabelArtifact) { a.TaskID = "other" }, func(a *orders.LabelArtifact) { a.LabelType = "small_label" },
	} {
		other := a
		change(&other)
		if _, err = protector.OpenOrderLabel(other); err == nil {
			t.Fatal("substituted label accepted")
		}
	}
	assertReorderedLabelChunksRejected(t, protector, a)
}

func assertReorderedLabelChunksRejected(t *testing.T, protector LabelProtector, a orders.LabelArtifact) {
	t.Helper()
	var err error
	var chunks [][]byte
	if err = json.Unmarshal(a.Ciphertext, &chunks); err != nil {
		t.Fatal(err)
	}
	chunks[0], chunks[1] = chunks[1], chunks[0]
	a.Ciphertext, err = json.Marshal(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = protector.OpenOrderLabel(a); err == nil {
		t.Fatal("reordered encrypted chunks accepted")
	}
}
