package config

import "testing"

func TestStockPublicationRequiresExplicitLoopbackSimulator(t *testing.T) {
	for _, tc := range []struct {
		flag, endpoint string
		allowed        bool
	}{
		{"", "http://127.0.0.1:1234", false}, {"false", "http://127.0.0.1:1234", false}, {"TRUE", "http://127.0.0.1:1234", false},
		{"true", "", false}, {"true", "https://api-seller.ozon.ru", false}, {"true", "http://localhost:1234", false}, {"true", "http://127.0.0.1.evil.example:1234", false},
		{"true", "http://127.0.0.1:1234/path", false}, {"true", "http://127.0.0.1:1234?secret=x", false}, {"true", "http://127.0.0.1:1234", true}, {"true", "http://[::1]:1234", true},
	} {
		t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", tc.flag)
		t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", tc.endpoint)
		_, actual := StockPublicationTestEndpoint()
		if actual != tc.allowed {
			t.Errorf("flag=%q endpoint=%q: got %v", tc.flag, tc.endpoint, actual)
		}
	}
}
