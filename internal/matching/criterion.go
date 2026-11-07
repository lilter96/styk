package matching

import "github.com/lilter96/styk/internal/domain"

type Criterion string

const (
	ArticleOrCode Criterion = "article_or_code"
	Article       Criterion = "article"
	Code          Criterion = "code"
)

func (c Criterion) Valid() bool { return c == ArticleOrCode || c == Article || c == Code }
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
