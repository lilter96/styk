package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

func TestCatalogCategoryTreeUsesNearestCategoryAndEnabledLeafTypes(t *testing.T) {
	t.Parallel()
	nodes := []ozon.CategoryNode{{DescriptionCategoryID: 10, CategoryName: "Товары", TypeID: 999, Children: []ozon.CategoryNode{
		{DescriptionCategoryID: 20, CategoryName: "Инструменты", Children: []ozon.CategoryNode{{TypeID: 30, TypeName: "Сверло"}, {TypeID: 31, TypeName: "Недоступный тип", Disabled: true}}},
		{DescriptionCategoryID: 21, CategoryName: "Недоступная категория", Disabled: true, Children: []ozon.CategoryNode{{TypeID: 32, TypeName: "Недоступный потомок"}}},
		{TypeName: "Нет типа"},
	}}, {TypeID: 40, TypeName: "Нет категории"}}
	items, err := flattenCatalogCategories(nodes)
	if err != nil || len(items) != 1 {
		t.Fatalf("enabled final types: %+v %v", items, err)
	}
	item := items[0]
	if item.ID != "20:30" || item.Name != "Товары / Инструменты / Сверло" {
		t.Fatalf("category/type identity or readable path lost: %+v", item)
	}
	if item.Attributes == nil || len(item.Attributes) != 0 || item.AttributesLoaded {
		t.Fatalf("unread attributes were represented as loaded: %+v", item)
	}
}

func nestedCatalogCategory(depth int) []ozon.CategoryNode {
	nodes := []ozon.CategoryNode{{TypeID: 2, TypeName: "Тип"}}
	for range depth {
		nodes = []ozon.CategoryNode{{DescriptionCategoryID: 1, CategoryName: "Категория", Children: nodes}}
	}
	return nodes
}

func TestCatalogCategoryTreeBoundsDepthAndTotalVisitedNodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		nodes   []ozon.CategoryNode
		invalid bool
	}{
		{"depth boundary", nestedCatalogCategory(16), false}, {"depth exceeded", nestedCatalogCategory(17), true},
		{"node boundary", make([]ozon.CategoryNode, 50000), false}, {"node count exceeded", make([]ozon.CategoryNode, 50001), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items, err := flattenCatalogCategories(tc.nodes)
			if tc.invalid {
				if !errors.Is(err, catalogsync.ErrInvalid) || items != nil {
					t.Fatalf("incomplete traversal became a category list: items=%d err=%v", len(items), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("documented traversal boundary rejected: %v", err)
			}
		})
	}
}

func categoryPageFixture(count int) []merchantCategory {
	items := make([]merchantCategory, 0, count)
	for index := range count {
		items = append(items, merchantCategory{ID: strconv.Itoa(index + 1), Name: fmt.Sprintf("Товары / Инструмент %03d", index+1)})
	}
	return items
}

func TestCatalogCategoryPaginationPreservesBoundsAndTerminalCursor(t *testing.T) {
	t.Parallel()
	items := categoryPageFixture(70)
	tests := []struct {
		name, cursor string
		limit, count int
		next, first  string
	}{
		{"first", "", 2, 2, "2", "1"}, {"continuation", "2", 2, 2, "4", "3"}, {"last", "68", 2, 2, "", "69"}, {"terminal", "70", 2, 0, "", ""},
		{"default", "", 0, 20, "20", "1"}, {"negative limit", "", -1, 20, "20", "1"}, {"oversized limit", "", 51, 20, "20", "1"}, {"maximum limit", "", 50, 50, "50", "1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page, next, err := pageCatalogCategories(items, "", tc.cursor, tc.limit)
			if err != nil || len(page) != tc.count || next != tc.next {
				t.Fatalf("count=%d next=%q err=%v", len(page), next, err)
			}
			if tc.count > 0 && page[0].ID != tc.first {
				t.Fatalf("wrong page boundary: first=%s", page[0].ID)
			}
		})
	}
}

func TestCatalogCategorySearchAppliesBeforeCursorAndHandlesUnicode(t *testing.T) {
	t.Parallel()
	items := []merchantCategory{{ID: "1", Name: "Товары / Сверло"}, {ID: "2", Name: "Товары / Пила"}, {ID: "3", Name: "Товары / СВЕРЛО"}}
	page, next, err := pageCatalogCategories(items, "  свЕРЛо  ", "1", 1)
	if err != nil || len(page) != 1 || page[0].ID != "3" || next != "" {
		t.Fatalf("filtered continuation: %+v next=%q err=%v", page, next, err)
	}
	if _, _, err := pageCatalogCategories(items, "сверло", "3", 1); !errors.Is(err, catalogsync.ErrInvalid) {
		t.Fatalf("cursor indexed unfiltered items: %v", err)
	}
	page, next, err = pageCatalogCategories(items, "нет совпадений", "", 1)
	if err != nil || page == nil || len(page) != 0 || next != "" {
		t.Fatalf("empty search: %+v next=%q err=%v", page, next, err)
	}
	if items[2].Name != "Товары / СВЕРЛО" {
		t.Fatal("search mutated source names")
	}
}

func TestCatalogCategoryPaginationRejectsInvalidCursors(t *testing.T) {
	t.Parallel()
	for _, cursor := range []string{"-1", "+1", " 1", "1.0", "1x", "4", "184467440737095516160"} {
		t.Run(cursor, func(t *testing.T) {
			t.Parallel()
			items, next, err := pageCatalogCategories(categoryPageFixture(3), "", cursor, 1)
			if !errors.Is(err, catalogsync.ErrInvalid) || items != nil || next != "" {
				t.Fatalf("invalid cursor became a page: %+v next=%q err=%v", items, next, err)
			}
		})
	}
}

func TestCatalogCategoryIdentityRequiresBothPositiveProviderIdentifiers(t *testing.T) {
	t.Parallel()
	selection, err := parseCatalogCategory("17029016:970778135")
	if err != nil || selection.DescriptionCategoryID != 17029016 || selection.TypeID != 970778135 {
		t.Fatalf("category/type pair lost: %+v %v", selection, err)
	}
	for _, raw := range []string{"", "1", "1:2:3", "0:2", "1:0", "-1:2", "1:-2", "1.5:2", "a:2", "1:a", "9223372036854775808:2"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := parseCatalogCategory(raw); !errors.Is(err, catalogsync.ErrInvalid) {
				t.Fatalf("invalid category pair accepted: %q %v", raw, err)
			}
		})
	}
}

func TestCatalogCategoryAttributeDescriptionPreservesRequiredTypeAndDictionaryFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind, want string
		dictionary int64
		required   bool
	}{
		{"string", "string", 0, true}, {"multiline", "string", 0, false}, {"integer", "number", 0, true}, {"decimal", "number", 0, false},
		{"number", "number", 0, false}, {"boolean", "boolean", 0, true}, {"string", "select", 99, true}, {"integer", "select", 99, false},
	}
	for _, tc := range tests {
		t.Run(tc.kind+strconv.FormatInt(tc.dictionary, 10), func(t *testing.T) {
			t.Parallel()
			category := describeCatalogCategory("7:8", []ozon.CategoryAttribute{{ID: 123, Name: "Характеристика", Type: tc.kind, Required: tc.required, DictionaryID: tc.dictionary}})
			if category.ID != "7:8" || !category.AttributesLoaded || len(category.Attributes) != 1 {
				t.Fatalf("loaded category schema lost: %+v", category)
			}
			attribute := category.Attributes[0]
			if attribute.ID != "123" || attribute.Name != "Характеристика" || attribute.Required != tc.required || attribute.Type != tc.want {
				t.Fatalf("provider schema changed: %+v", attribute)
			}
			if attribute.Dictionary != (tc.dictionary > 0) || attribute.OptionsIncomplete != (tc.dictionary > 0) || len(attribute.Options) != 0 {
				t.Fatalf("dictionary values fabricated or marked complete: %+v", attribute)
			}
		})
	}
}

func TestCatalogCategoryPermissionErrorsHaveSafeDistinctResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unauthenticated", &ozon.APIError{Status: 401}, 422, "CATALOG_CATEGORY_ACCESS_REQUIRED"},
		{"wrapped permission", fmt.Errorf("read category: %w", &ozon.APIError{Status: 403}), 422, "CATALOG_CATEGORY_ACCESS_REQUIRED"},
		{"rate limit", &ozon.APIError{Status: 429}, 503, "CATALOG_SERVICE_UNAVAILABLE"},
		{"invalid", catalogsync.ErrInvalid, 400, "CATALOG_INVALID_REQUEST"},
		{"unknown", errors.New("fixture-sensitive-detail"), 503, "CATALOG_SERVICE_UNAVAILABLE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			catalogCategoryError(response, tc.err)
			assertCatalogCategoryHTTPError(t, response, tc.status, tc.code)
		})
	}
}

func assertCatalogCategoryHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || decoded.Error != code || strings.Contains(response.Body.String(), "fixture-sensitive-detail") {
		t.Fatalf("unsafe or inaccurate HTTP error: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCatalogAttributeRequestRejectsMalformedIdentifiersBeforeProviderResolution(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, query, code string }{
		{"category", "category_id=invalid&attribute_id=1", "CATALOG_INVALID_REQUEST"},
		{"attribute", "category_id=7:8&attribute_id=0", "CATALOG_INVALID_ATTRIBUTE"},
		{"cursor", "category_id=7:8&attribute_id=1&cursor=-1", "CATALOG_INVALID_PAGE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			(&catalogAPI{}).attributes(response, httptest.NewRequest(http.MethodGet, "/api/catalog-sync/attributes?"+tc.query, nil))
			assertCatalogCategoryHTTPError(t, response, 400, tc.code)
		})
	}
}
