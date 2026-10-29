package moysklad

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogValidationReturnsFieldErrorsBeforeIntentExists(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c := s.writer(t)
	cmd := catalog.Command{Scope: catalogTestScope(), Action: catalog.ActionCreate, Target: catalog.Identity{Account: catalogTestAccountRef(), EntityType: "product"}, Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Reviewable name"), catalog.FieldDescription: catalog.Text("Reviewable description")}}
	problems, err := c.ValidateCatalog(context.Background(), cmd)
	if err != nil || len(problems) != 0 {
		t.Fatalf("provisional reviewable creation rejected: %+v %v", problems, err)
	}
	cmd.Values[catalog.FieldName] = catalog.Clear()
	cmd.Values[catalog.FieldImages] = catalog.Text("Do not leak this value")
	problems, err = c.ValidateCatalog(context.Background(), cmd)
	if err != nil || len(problems) < 2 {
		t.Fatalf("missing field validation errors: %+v %v", problems, err)
	}
	for _, problem := range problems {
		if problem.Field == "" || problem.Code == "" || problem.Message != "The selected catalog facts are not supported by MoySklad." {
			t.Fatalf("unsafe or unlocated validation error: %+v", problem)
		}
	}
	writes, _, events := s.state()
	if writes != 0 || len(events) != 0 {
		t.Fatal("local validation bound or dispatched external work")
	}
}

func TestCatalogValidationRequiresTruthfulVariantCreationFacts(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	cmd := catalog.Command{Scope: catalogTestScope(), Action: catalog.ActionCreate, Target: catalog.Identity{Account: catalogTestAccountRef(), EntityType: "variant"}}
	problems, err := s.writer(t).ValidateCatalog(context.Background(), cmd)
	if err != nil || len(problems) != 2 || problems[0].Field != catalog.FieldParent || problems[1].Field != catalog.FieldAttributes {
		t.Fatalf("variant could be reviewed without its actual parent and characteristics: %+v %v", problems, err)
	}
}
