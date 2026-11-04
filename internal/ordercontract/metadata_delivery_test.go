package ordercontract_test

import (
	"testing"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/ordercontract"
)

func TestDeliveryMetadataReviewedFieldsNeverPromoteCompleteSchemaEvidence(t *testing.T) {
	t.Parallel()
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, contract := range manifest.Contracts {
		if contract.Endpoint != "/v2/warehouse/list" && contract.Endpoint != "/v2/delivery-method/list" {
			continue
		}
		count++
		if len(contract.Fields) < 5 || contract.SchemaStatus != "partial_unverified" {
			t.Fatalf("placeholder or false full verification: %s %s", contract.Endpoint, contract.Direction)
		}
		for _, field := range contract.Fields {
			if field.Verification != "official_field_verified" {
				t.Fatal("reviewed field still lacks truthful evidence classification")
			}
		}
	}
	if count != 4 {
		t.Fatal("four metadata contract directions required")
	}
	evidence, err := delivery.Evidence(manifest)
	if err != nil || evidence.SchemaVerified || !evidence.VersionsVerified {
		t.Fatalf("subset field review changed publication authority: %+v %v", evidence, err)
	}
}

func TestDeliveryMetadataExecutableSelectedFieldTypes(t *testing.T) {
	t.Parallel()
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, endpoint, direction, payload string
		valid                              bool
	}{
		{"warehouse request exact ID strings", "/v2/warehouse/list", "request", `{"limit":100,"cursor":"","warehouse_ids":["9007199254740993"]}`, true},
		{"warehouse request needs limit", "/v2/warehouse/list", "request", `{"cursor":""}`, false},
		{"warehouse request IDs not JSON numbers", "/v2/warehouse/list", "request", `{"limit":100,"warehouse_ids":[9007199254740993]}`, false},
		{"warehouse top-level arrays", "/v2/warehouse/list", "response", `{"warehouses":[],"has_next":false}`, true},
		{"warehouse boolean not string", "/v2/warehouse/list", "response", `{"warehouses":[{"warehouse_id":9007199254740993,"is_rfbs":"false"}],"has_next":false}`, false},
		{"warehouse unknown address excluded subset", "/v2/warehouse/list", "response", `{"warehouses":[{"warehouse_id":7,"address_info":{"address":"synthetic"}}],"has_next":false}`, false},
		{"method cursor request", "/v2/delivery-method/list", "request", `{"limit":100,"cursor":"opaque+cursor=","sort_dir":"ASC","filter":{"status":["ACTIVE"],"warehouse_ids":["9007199254740993"]}}`, true},
		{"method legacy offset drift", "/v2/delivery-method/list", "request", `{"limit":100,"offset":0}`, false},
		{"method top-level current response", "/v2/delivery-method/list", "response", `{"delivery_methods":[{"id":9007199254740995,"warehouse_id":9007199254740993,"status":"ACTIVE","is_express":true}],"has_next":true,"cursor":"next"}`, true},
		{"method wrapper drift", "/v2/delivery-method/list", "response", `{"result":[],"has_next":false}`, false},
		{"method missing different from null", "/v2/delivery-method/list", "response", `{"delivery_methods":null,"has_next":false}`, false},
		{"method opaque cursor must string", "/v2/delivery-method/list", "response", `{"delivery_methods":[],"has_next":true,"cursor":9007199254740993}`, false},
		{"method ID cannot fractional", "/v2/delivery-method/list", "response", `{"delivery_methods":[{"id":1.5,"warehouse_id":7}],"has_next":false}`, false},
		{"method status unknown remains typed observation", "/v2/delivery-method/list", "response", `{"delivery_methods":[{"id":8,"warehouse_id":7,"status":"future_lifecycle"}],"has_next":false}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := manifest.Validate(tc.endpoint, tc.direction, []byte(tc.payload))
			if report.Valid != tc.valid || report.SchemaVerified {
				t.Fatalf("metadata type/coverage assertion failed: %+v", report)
			}
		})
	}
}
