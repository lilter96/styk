package console

import (
	"fmt"
	"net/http"
	"strconv"
)

func productPagination(r *http.Request, offset int) (int, int, error) {
	size := 50
	q := r.URL.Query()
	if q.Has("pageSize") {
		value, err := strconv.Atoi(q.Get("pageSize"))
		if err != nil || value < 1 || value > 100 {
			return 0, 0, fmt.Errorf("invalid page size")
		}
		size = value
	}
	if q.Has("page") {
		value, err := strconv.Atoi(q.Get("page"))
		if err != nil || value < 1 || value > 100001 {
			return 0, 0, fmt.Errorf("invalid page")
		}
		offset = (value - 1) * size
	}
	if offset > 100000 {
		return 0, 0, fmt.Errorf("page offset exceeded")
	}
	return offset, size, nil
}
