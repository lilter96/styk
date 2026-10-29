package postgres

import (
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

func TestConfirmationFingerprintBindsSemanticRequest(t *testing.T) {
	base := domain.MappingConfirmationRequest{ReviewID: strings.Repeat("a", 32), SessionID: "one", IdempotencyKey: "first", Acknowledged: true, Explanation: "synthetic"}
	original, err := confirmationFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	same := base
	same.IdempotencyKey = "another"
	same.SessionID = "another-session"
	h, err := confirmationFingerprint(same)
	if err != nil || h != original {
		t.Fatal("key/session should be scoped separately from payload fingerprint")
	}
	for name, mutate := range map[string]func(*domain.MappingConfirmationRequest){
		"review":          func(r *domain.MappingConfirmationRequest) { r.ReviewID = strings.Repeat("b", 32) },
		"acknowledgement": func(r *domain.MappingConfirmationRequest) { r.Acknowledged = false },
		"explanation":     func(r *domain.MappingConfirmationRequest) { r.Explanation += " changed" },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			hash, err := confirmationFingerprint(r)
			if err != nil || hash == original {
				t.Fatal("request content was not bound")
			}
		})
	}
}
