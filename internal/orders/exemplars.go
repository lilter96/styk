package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const (
	ActionExemplarPrepare Action = "posting_exemplar_prepare"
	ActionExemplarSet     Action = "posting_exemplar_set"
	MaxExemplarBytes             = 120 << 10
)

type ExemplarMark struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type ExemplarInput struct {
	ClearMarkTypes []string              `json:"clear_mark_types,omitempty"`
	ID             string                `json:"id"`
	GTD            Field[string]         `json:"gtd"`
	GTDAbsent      Field[bool]           `json:"gtd_absent"`
	RNPT           Field[string]         `json:"rnpt"`
	RNPTAbsent     Field[bool]           `json:"rnpt_absent"`
	Marks          Field[[]ExemplarMark] `json:"marks"`
}
type ExemplarProductInput struct {
	SKU       string          `json:"sku"`
	Exemplars []ExemplarInput `json:"exemplars"`
}
type ExemplarSetInput struct {
	Products []ExemplarProductInput `json:"products"`
}
type ExemplarProduct struct {
	UnsupportedFields []string                               `json:"unsupported_fields,omitempty"`
	SKU               string                                 `json:"sku"`
	Quantity          int64                                  `json:"quantity"`
	Required          map[PackingRequirementKind]Field[bool] `json:"required"`
	Exemplars         []ExemplarInput                        `json:"exemplars"`
}
type ExemplarSnapshot struct {
	PostingNumber string            `json:"posting_number"`
	Products      []ExemplarProduct `json:"products"`
}
type PublicExemplarProduct struct {
	UnsupportedFields []string                               `json:"unsupported_fields,omitempty"`
	SKU               string                                 `json:"sku"`
	Quantity          int64                                  `json:"quantity"`
	Required          map[PackingRequirementKind]Field[bool] `json:"required"`
	IDs               []string                               `json:"ids"`
}
type ExemplarPublic struct {
	ProofValidUntil   *time.Time              `json:"proof_valid_until,omitempty"`
	Scope             Scope                   `json:"scope"`
	SourceRevision    string                  `json:"source_revision"`
	ProofWorkID       string                  `json:"proof_work_id,omitempty"`
	WorkID            string                  `json:"work_id"`
	PostingNumber     string                  `json:"posting_number"`
	TargetFingerprint string                  `json:"target_fingerprint"`
	Products          []PublicExemplarProduct `json:"products"`
	ExpiresAt         time.Time               `json:"expires_at"`
}
type ExemplarSecret struct {
	Ready                                            bool
	Scope                                            Scope
	WorkID, PostingNumber, TargetFingerprint, Digest string
	CredentialRevision                               int64
	Ciphertext                                       []byte `json:"-"`
	Public                                           ExemplarPublic
	CreatedAt, ExpiresAt                             time.Time
}
type ExemplarCheck struct {
	ID        string `json:"id"`
	GTD, RNPT string
	Marks     []string
	Errors    []string `json:"errors,omitempty"`
}
type ExemplarStatusProduct struct {
	UnsupportedFields []string
	SKU               string
	Exemplars         []ExemplarInput
	Checks            []ExemplarCheck
}
type ExemplarStatus struct {
	PostingNumber, Status string
	Products              []ExemplarStatusProduct
}
type ExemplarEvidence struct {
	PackingReady       bool                `json:"packing_ready"`
	CredentialRevision int64               `json:"credential_revision"`
	RequirementsHash   string              `json:"requirements_hash"`
	Scope              Scope               `json:"scope"`
	Verified           bool                `json:"verified"`
	TargetFingerprint  string              `json:"target_fingerprint"`
	Status             string              `json:"status"`
	SnapshotID         string              `json:"snapshot_id"`
	IDs                map[string][]string `json:"ids"`
	CheckedAt          time.Time           `json:"checked_at"`
}

type ExemplarStatusReader interface {
	ReadOrderExemplarStatus(context.Context, Scope, string) (ExemplarStatus, error)
}
type ExemplarSecretReader interface {
	OrderExemplarSecret(context.Context, Scope, string) (ExemplarSecret, error)
}
type ExemplarReader interface {
	ReadOrderExemplars(context.Context, Scope, string) (ExemplarSnapshot, ExemplarSecret, error)
}
type ExemplarSealer interface {
	SealOrderExemplars(Work, ExemplarSnapshot, time.Time) (ExemplarSecret, error)
}

func (a Action) IsExemplar() bool { return a == ActionExemplarPrepare || a == ActionExemplarSet }

// PackingTargetFingerprint excludes evolving requirement/status metadata while
// binding the exact merchant, posting, warehouse, delivery method and unit pool.
func PackingTargetFingerprint(p Posting) (string, error) {
	if p.Validate() != nil || !p.WarehouseID.IsKnown() || !p.DeliveryMethodID.IsKnown() {
		return "", ErrInvalid
	}
	lines := slices.Clone(p.Lines)
	slices.SortFunc(lines, func(a, b PostingLine) int { return strings.Compare(a.Identity.Key(), b.Identity.Key()) })
	type line struct {
		Identity LineIdentity
		Quantity Decimal
	}
	values := make([]line, 0, len(lines))
	for _, v := range lines {
		values = append(values, line{v.Identity, v.Quantity})
	}
	raw, err := json.Marshal(struct {
		Scope                             Scope
		Order, Posting, Warehouse, Method string
		Lines                             []line
	}{p.Scope, p.CommercialOrderID, p.PostingNumber, *p.WarehouseID.Value, *p.DeliveryMethodID.Value, values})
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s ExemplarSnapshot) Public(work Work, now time.Time) (ExemplarPublic, error) {
	result := ExemplarPublic{Scope: work.Scope, SourceRevision: work.Command.ExpectedSourceRevision, WorkID: work.ID, PostingNumber: s.PostingNumber, Products: make([]PublicExemplarProduct, 0, len(s.Products)), ExpiresAt: now.Add(24 * time.Hour)}
	for _, p := range s.Products {
		v := PublicExemplarProduct{UnsupportedFields: slices.Clone(p.UnsupportedFields), SKU: p.SKU, Quantity: p.Quantity, Required: p.Required, IDs: make([]string, 0, len(p.Exemplars))}
		for _, u := range p.Exemplars {
			v.IDs = append(v.IDs, u.ID)
		}
		slices.Sort(v.IDs)
		result.Products = append(result.Products, v)
	}
	return result, nil
}
