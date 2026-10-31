package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ConnectionSettings describes a registered connection, independently of its secret.
type ConnectionSettings struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Revision    int64  `json:"revision"`
}

func (s ConnectionSettings) IsValid() bool {
	return validConnectionText(s.Name, 120, false) && validConnectionText(s.Description, 1000, true) && s.Revision >= 0
}

func validConnectionText(value string, limit int, empty bool) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > limit {
		return false
	}
	if value == "" {
		return empty
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}
