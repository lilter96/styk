package ordercontract

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

type fieldNode struct {
	field    *Field
	children map[string]*fieldNode
	element  *fieldNode
}

// Validate checks a known snapshot. A valid partial schema is not proof of API support.
func (m Manifest) Validate(endpoint, direction string, data []byte) Report {
	report := Report{Issues: make([]Issue, 0), DriftPaths: make([]string, 0)}
	for _, contract := range m.Contracts {
		if contract.Endpoint != endpoint || contract.Direction != direction {
			continue
		}
		report.SchemaVerified = contract.SchemaStatus == "official_verified"
		return contract.validate(data, report)
	}
	report.add("$", "unknown_contract")
	return report
}

func (c Contract) validate(data []byte, report Report) Report {
	root, err := c.tree()
	if err != nil {
		report.add("$", "invalid_manifest")
		return report
	}
	value, err := decodePayload(data)
	if err != nil {
		report.add("$", "invalid_json")
		return report
	}
	root.check(value, &report)
	slices.SortFunc(report.Issues, func(a, b Issue) int { return strings.Compare(a.Path+":"+a.Code, b.Path+":"+b.Code) })
	slices.Sort(report.DriftPaths)
	report.Valid = len(report.Issues) == 0
	return report
}

func (c Contract) tree() (*fieldNode, error) {
	root := &fieldNode{children: make(map[string]*fieldNode)}
	for i := range c.Fields {
		field := &c.Fields[i]
		if !fieldPath.MatchString(field.Path) {
			return nil, manifestError()
		}
		node := root
		for _, segment := range strings.Split(strings.ReplaceAll(strings.TrimPrefix(field.Path, "$"), "[]", ".[]"), ".") {
			if segment != "" {
				node = node.child(segment)
			}
		}
		if node.field != nil {
			return nil, manifestError()
		}
		node.field = field
	}
	if !root.complete() {
		return nil, manifestError()
	}
	return root, nil
}

func (n *fieldNode) child(segment string) *fieldNode {
	if segment == "[]" {
		if n.element == nil {
			n.element = &fieldNode{children: make(map[string]*fieldNode)}
		}
		return n.element
	}
	if n.children[segment] == nil {
		n.children[segment] = &fieldNode{children: make(map[string]*fieldNode)}
	}
	return n.children[segment]
}

func (n *fieldNode) complete() bool {
	if n.field == nil || (len(n.children) > 0 && !slices.Contains(n.field.Types, "object")) {
		return false
	}
	if slices.Contains(n.field.Types, "array") && n.element == nil {
		return false
	}
	if n.element != nil && (!slices.Contains(n.field.Types, "array") || !n.element.complete()) {
		return false
	}
	for _, child := range n.children {
		if !child.complete() {
			return false
		}
	}
	return true
}

func (n *fieldNode) check(value any, report *Report) {
	if len(report.Issues) >= maxIssues {
		return
	}
	if value == nil {
		if !n.field.Nullable {
			report.add(n.field.Path, "null_not_allowed")
		}
		return
	}
	if !n.accepts(value) {
		report.add(n.field.Path, "wrong_type")
		return
	}
	switch value := value.(type) {
	case map[string]any:
		n.checkObject(value, report)
	case []any:
		for _, element := range value {
			if n.element != nil {
				n.element.check(element, report)
			}
		}
	}
}

func (n *fieldNode) checkObject(object map[string]any, report *Report) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		child := n.children[key]
		if child == nil {
			path := n.field.Path + ".<unknown:" + digest([]byte(key))[:16] + ">"
			report.drift(path)
			continue
		}
		child.check(object[key], report)
	}
	children := make([]string, 0, len(n.children))
	for key := range n.children {
		children = append(children, key)
	}
	slices.Sort(children)
	for _, key := range children {
		child := n.children[key]
		if _, present := object[key]; !present && child.field.Presence == "required" {
			report.add(child.field.Path, "missing_required")
		}
	}
}

func (n *fieldNode) accepts(value any) bool {
	kind := ""
	switch value := value.(type) {
	case map[string]any:
		kind = "object"
	case []any:
		kind = "array"
	case string:
		kind = "string"
	case bool:
		kind = "boolean"
	case json.Number:
		if slices.Contains(n.field.Types, "integer") {
			_, err := strconv.ParseInt(string(value), 10, 64)
			if err == nil {
				return true
			}
		}
		kind = "number"
	}
	return slices.Contains(n.field.Types, kind)
}

func (r *Report) add(path, code string) {
	if len(r.Issues) < maxIssues && !slices.Contains(r.Issues, Issue{Path: path, Code: code}) {
		r.Issues = append(r.Issues, Issue{Path: path, Code: code})
	}
}

func (r *Report) drift(path string) {
	if len(r.Issues) >= maxIssues || slices.Contains(r.DriftPaths, path) {
		return
	}
	r.DriftPaths = append(r.DriftPaths, path)
	r.add(path, "unknown_key")
}
