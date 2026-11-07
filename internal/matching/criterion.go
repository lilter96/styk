package matching

import "github.com/lilter96/styk/internal/domain"

type Criterion string

const (
	ArticleOrCode Criterion = "article_or_code"
	Article       Criterion = "article"
	Code          Criterion = "code"
)

var criterionFields = map[Criterion][]string{ArticleOrCode: {"article", "code"}, Article: {"article"}, Code: {"code"}}

func (c Criterion) Valid() bool      { _, ok := criterionFields[c]; return ok }
func (c Criterion) Fields() []string { return criterionFields[c.Resolved()] }
func (c Criterion) Resolved() Criterion {
	if c == "" {
		return ArticleOrCode
	}
	return c
}
func (c Criterion) target(t domain.MatchTarget) domain.MatchTarget {
	if c == Article {
		t.Code = ""
	}
	if c == Code {
		t.Article = ""
	}
	return t
}
