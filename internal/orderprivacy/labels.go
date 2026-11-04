package orderprivacy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

const labelChunkBytes = 64 << 10

type LabelProtector struct{ Vault *credentials.Vault }

func (p LabelProtector) SealOrderLabel(work orders.Work, task orders.ActionTask, data []byte, now time.Time) (orders.LabelArtifact, error) {
	a := orders.LabelArtifact{Scope: work.Scope, WorkID: work.ID, TaskID: task.ID, LabelType: task.Type, Size: len(data), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if p.Vault == nil || !validLabelPDF(data) {
		return a, orders.ErrBlocked
	}
	a.Digest = hex.EncodeToString(p.Vault.Digest(data))
	chunks := make([][]byte, 0, (len(data)+labelChunkBytes-1)/labelChunkBytes)
	for start := 0; start < len(data); start += labelChunkBytes {
		cipher, err := p.Vault.EncryptRecord(labelRecord(a, len(chunks)), data[start:min(start+labelChunkBytes, len(data))])
		if err != nil {
			return a, err
		}
		chunks = append(chunks, cipher)
	}
	var err error
	a.Ciphertext, err = json.Marshal(chunks)
	if err != nil {
		return a, orders.ErrInvalid
	}
	return a, a.Validate()
}

func labelRecord(a orders.LabelArtifact, index int) credentials.RecordScope {
	id := identityDigest(a.Scope.MoyskladAccount, a.WorkID, a.TaskID, a.LabelType, a.Digest, strconv.Itoa(index))
	return credentials.RecordScope{Scope: credentials.Scope{Tenant: a.Scope.Tenant, Provider: "ozon", Account: a.Scope.OzonAccount}, Purpose: "order-artifact", RecordID: id, Revision: 1}
}

func (p LabelProtector) OpenOrderLabel(a orders.LabelArtifact) ([]byte, error) {
	if p.Vault == nil || a.Validate() != nil {
		return nil, orders.ErrBlocked
	}
	var chunks [][]byte
	if json.Unmarshal(a.Ciphertext, &chunks) != nil || len(chunks) != (a.Size+labelChunkBytes-1)/labelChunkBytes {
		return nil, orders.ErrInvalid
	}
	data := make([]byte, 0, a.Size)
	for index, cipher := range chunks {
		plain, err := p.Vault.DecryptRecord(labelRecord(a, index), cipher)
		if err != nil || len(plain) > labelChunkBytes || len(data)+len(plain) > a.Size {
			return nil, orders.ErrInvalid
		}
		data = append(data, plain...)
	}
	if len(data) != a.Size || !validLabelPDF(data) || hex.EncodeToString(p.Vault.Digest(data)) != a.Digest {
		return nil, orders.ErrInvalid
	}
	return data, nil
}

func validLabelPDF(data []byte) bool {
	return len(data) > 0 && len(data) <= orders.MaxLabelBytes && bytes.HasPrefix(data, []byte("%PDF-")) && bytes.HasSuffix(bytes.TrimSpace(data), []byte("%%EOF"))
}
