package orderactions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

const (
	RFBSHybridNoticeURL     = "https://dev.ozon.ru/news/638-Novyi-protsess-dostavki-zakazov-v-punkty-vydachi-PVZ/"
	RFBSHybridNoticeDate    = "2025-10-20"
	RFBSHybridUpdateDate    = "2025-10-30"
	RFBSNoticeCheckedAt     = "2026-10-30"
	RFBSHybridNoticeSummary = "Official notice 638 and its 2025-10-30 update distinguish non-express hybrid Ozon pickup-point delivery. Positive tpl_provider_id identifies the Ozon partner route. A null provider_id identifies seller delivery; a label is mandatory using posting available actions, and the seller changes only Delivering before automatic later statuses. The notice does not verify complete request schemas, action tokens or predecessor status enums."
	RFBSTrackingNoticeURL   = "https://dev.ozon.ru/news/651-Vvedenie-PIN-kodov-dlia-realFBS-otpravlenii-v-Seller-API/"
)

// RFBSHybridSource hashes the evidence summary, not inaccessible raw HTML or an
// API schema. The canonical notice is readable; its short /news/638/ URL is not.
func RFBSHybridSource() ordercontract.Source {
	sum := sha256.Sum256([]byte(RFBSHybridNoticeSummary))
	return ordercontract.Source{ID: "ozon_news638", URL: RFBSHybridNoticeURL, CheckedAt: RFBSNoticeCheckedAt,
		Status: "official_notice_verified", SHA256: hex.EncodeToString(sum[:]), HashBasis: "evidence_summary_utf8", Evidence: RFBSHybridNoticeSummary}
}

func rfbsContract(manifest ordercontract.Manifest, endpoint string) (string, []Diagnostic, error) {
	contracts := make([]ordercontract.Contract, 0, 2)
	diagnostics := make([]Diagnostic, 0, 2)
	for _, direction := range []string{"request", "response"} {
		contract, found := rfbsFindContract(manifest, endpoint, direction)
		if !found {
			diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_"+direction+"_contract_missing", "contract."+direction))
			continue
		}
		contracts = append(contracts, contract)
		if contract.SchemaStatus != "official_verified" || contract.Coverage != "complete" {
			diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_"+direction+"_contract_unverified", "contract."+direction))
		}
	}
	raw, err := json.Marshal(contracts)
	if err != nil {
		return "", nil, orders.ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), diagnostics, nil
}

func rfbsFindContract(manifest ordercontract.Manifest, endpoint, direction string) (ordercontract.Contract, bool) {
	for _, contract := range manifest.Contracts {
		if contract.Endpoint == endpoint && contract.Direction == direction {
			return contract, true
		}
	}
	return ordercontract.Contract{}, false
}
