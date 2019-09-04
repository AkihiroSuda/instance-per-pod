package jsonpatch

import (
	"strings"
)

type Op struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

const (
	OpAdd = "add"
)

func EscapeRFC6901(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}
