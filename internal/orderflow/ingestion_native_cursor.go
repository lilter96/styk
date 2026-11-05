package orderflow

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

const nativePointCursorPrefix = "native-points-v2."

// Alternate bounded parent/document pages so a large parent catalog cannot
// starve document refresh. The two offsets belong to this existing checkpoint.
type nativePointCursor struct {
	Documents     bool   `json:"documents"`
	ParentAfter   string `json:"parent_after"`
	DocumentAfter string `json:"document_after"`
	ParentsDone   bool   `json:"parents_done"`
	DocumentsDone bool   `json:"documents_done"`
}

func decodeNativePointCursor(raw string) (nativePointCursor, error) {
	if !strings.HasPrefix(raw, nativePointCursorPrefix) {
		if len(raw) > 512 {
			return nativePointCursor{}, orders.ErrInvalid
		}
		return nativePointCursor{ParentAfter: raw}, nil // Existing checkpoints.
	}
	if len(raw) > 2048 {
		return nativePointCursor{}, orders.ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, nativePointCursorPrefix))
	var cursor nativePointCursor
	if err != nil || json.Unmarshal(b, &cursor) != nil || len(cursor.ParentAfter) > 512 || len(cursor.DocumentAfter) > 512 || cursor.Documents && cursor.DocumentsDone || !cursor.Documents && cursor.ParentsDone {
		return cursor, orders.ErrInvalid
	}
	return cursor, nil
}

func (cursor nativePointCursor) next(next string, documentsAvailable bool) (string, error) {
	if !documentsAvailable {
		return next, nil // Preserve parent-only providers and test stores.
	}
	if cursor.Documents {
		cursor.DocumentAfter, cursor.DocumentsDone = next, next == ""
		cursor.Documents = cursor.ParentsDone
	} else {
		cursor.ParentAfter, cursor.ParentsDone = next, next == ""
		cursor.Documents = !cursor.DocumentsDone
	}
	if cursor.ParentsDone && cursor.DocumentsDone {
		return "", nil
	}
	b, err := json.Marshal(cursor)
	return nativePointCursorPrefix + base64.RawURLEncoding.EncodeToString(b), err
}
