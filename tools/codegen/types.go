package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Field is a struct field of a generated model type.
type Field struct {
	Name string
	Type string
	JSON string
	Doc  string
	Omit bool
}

// Struct is a generated model type.
type Struct struct {
	Name   string
	Doc    string
	Fields []Field
}

func (s *Struct) signature() string {
	var b strings.Builder
	for _, f := range s.Fields {
		fmt.Fprintf(&b, "%s %s %s;", f.Name, f.Type, f.JSON)
	}
	return b.String()
}

// Types collects generated model types under globally unique names.
type Types struct {
	structs map[string]*Struct
	names   uniqueNamer
	bySig   map[string]string
}

func newTypes(reserved ...string) *Types {
	t := &Types{structs: map[string]*Struct{}, names: uniqueNamer{}, bySig: map[string]string{}}
	for _, r := range reserved {
		t.names[r] = true
	}
	return t
}

// add registers s, reusing an existing identical type registered under the
// same base name when possible, and returns the final type name.
func (t *Types) add(s *Struct) string {
	sig := s.Name + "|" + s.signature()
	if n, ok := t.bySig[sig]; ok {
		return n
	}
	s.Name = t.names.name(s.Name)
	t.structs[s.Name] = s
	t.bySig[sig] = s.Name
	return s.Name
}

func (t *Types) sorted() []*Struct {
	out := make([]*Struct, 0, len(t.structs))
	for _, k := range sortedKeys(t.structs) {
		out = append(out, t.structs[k])
	}
	return out
}

// ---------------------------------------------------------------------------
// Inference from v1 JSON response examples.

type conflict struct{}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// decodeExample parses a JSON example keeping number literals.
func decodeExample(s string) (any, error) {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// merge combines two example values describing the same location.
func merge(a, b any) any {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return conflict{}
		}
		out := map[string]any{}
		for k, v := range x {
			out[k] = v
		}
		for k, v := range y {
			if cur, ok := out[k]; ok {
				out[k] = merge(cur, v)
			} else {
				out[k] = v
			}
		}
		return out
	case []any:
		y, ok := b.([]any)
		if !ok {
			return conflict{}
		}
		return append(append([]any{}, x...), y...)
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return conflict{}
		}
		if strings.ContainsAny(string(y), ".eE") {
			return y
		}
		return x
	case string, bool:
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", b) {
			return conflict{}
		}
		return a
	}
	return conflict{}
}

// singular is a light singularisation for element type names.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "xes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ss"):
		return s
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}

// paging is the common v1 pagination object, shared by name.
var pagingKeys = map[string]bool{"pageIndex": true, "pageSize": true, "total": true}

// isIDKey reports whether a JSON key names an identifier.
func isIDKey(k string) bool {
	return k == "id" || k == "uuid" || strings.HasSuffix(k, "Id") || strings.HasSuffix(k, "Uuid") || strings.HasSuffix(k, "ID")
}

// infer returns the Go type for an example value, registering structs.
func (t *Types) infer(name string, v any, overrides map[string]string) string {
	switch x := v.(type) {
	case nil, conflict:
		return "json.RawMessage"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return "float64"
		}
		return "int64"
	case []any:
		if len(x) == 0 {
			return "[]json.RawMessage"
		}
		var m any
		for _, e := range x {
			m = merge(m, e)
		}
		return "[]" + t.infer(singular(name), m, overrides)
	case map[string]any:
		if len(x) == 0 {
			return "map[string]json.RawMessage"
		}
		isMap := false
		for k := range x {
			if !identRE.MatchString(k) {
				isMap = true
			}
		}
		if isMap {
			var m any
			for _, k := range sortedKeys(x) {
				m = merge(m, x[k])
			}
			return "map[string]" + t.infer(name+"Value", m, overrides)
		}
		if len(x) == len(pagingKeys) {
			ok := true
			for k := range x {
				ok = ok && pagingKeys[k]
			}
			if ok {
				return "*Paging"
			}
		}
		s := &Struct{Name: name}
		fn := uniqueNamer{}
		for _, k := range sortedKeys(x) {
			fname := fn.name(goName(k))
			ft, ok := overrides[name+"."+k]
			if !ok {
				ft = t.infer(name+goName(k), x[k], overrides)
				if isIDKey(k) && (ft == "int64" || ft == "string") {
					ft = "ID"
				}
			}
			s.Fields = append(s.Fields, Field{Name: fname, Type: ft, JSON: k, Omit: true})
		}
		return "*" + t.add(s)
	}
	return "json.RawMessage"
}

// ---------------------------------------------------------------------------
// OpenAPI schemas (v2).

type openapi struct {
	Paths      map[string]map[string]*operation `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
	OperationID string `json:"operationId"`
	Deprecated  bool   `json:"deprecated"`
	Internal    any    `json:"x-sonar-internal"`
	Parameters  []struct {
		Name        string         `json:"name"`
		In          string         `json:"in"`
		Required    bool           `json:"required"`
		Description string         `json:"description"`
		Deprecated  bool           `json:"deprecated"`
		Schema      map[string]any `json:"schema"`
	} `json:"parameters"`
	RequestBody *struct {
		Required bool                                       `json:"required"`
		Content  map[string]struct{ Schema map[string]any } `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Description string                                     `json:"description"`
		Content     map[string]struct{ Schema map[string]any } `json:"content"`
	} `json:"responses"`
}

type schemaGen struct {
	t        *Types
	schemas  map[string]map[string]any
	done     map[string]string // schema name -> Go type name
	request  map[string]bool   // schemas reachable from request bodies
	inflight map[string]bool
}

func refName(s map[string]any) string {
	if r, ok := s["$ref"].(string); ok {
		return strings.TrimPrefix(r, "#/components/schemas/")
	}
	return ""
}

func schemaTypes(s map[string]any) (types []string, nullable bool) {
	switch v := s["type"].(type) {
	case string:
		types = []string{v}
	case []any:
		for _, x := range v {
			if x == "null" {
				nullable = true
			} else {
				types = append(types, x.(string))
			}
		}
	}
	if n, ok := s["nullable"].(bool); ok && n {
		nullable = true
	}
	return
}

// markRequest marks every schema reachable from s as a request schema.
func (g *schemaGen) markRequest(s map[string]any) {
	if s == nil {
		return
	}
	if r := refName(s); r != "" {
		if g.request[r] {
			return
		}
		g.request[r] = true
		g.markRequest(g.schemas[r])
		return
	}
	if it, ok := s["items"].(map[string]any); ok {
		g.markRequest(it)
	}
	if ap, ok := s["additionalProperties"].(map[string]any); ok {
		g.markRequest(ap)
	}
	for _, k := range []string{"allOf", "oneOf", "anyOf"} {
		if parts, ok := s[k].([]any); ok {
			for _, p := range parts {
				g.markRequest(p.(map[string]any))
			}
		}
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for _, p := range props {
			g.markRequest(p.(map[string]any))
		}
	}
}

// goType maps a schema to a Go type. hint names inline object types.
// isPtrCtx requests pointer form for optional scalars (request schemas).
func (g *schemaGen) goType(s map[string]any, hint string, request bool) string {
	if r := refName(s); r != "" {
		name := g.component(r)
		if strings.HasPrefix(name, "map[") || strings.HasPrefix(name, "[]") || name == "json.RawMessage" || name == "string" {
			return name
		}
		return "*" + name
	}
	if _, ok := s["oneOf"]; ok {
		return "json.RawMessage"
	}
	if _, ok := s["anyOf"]; ok {
		return "json.RawMessage"
	}
	if parts, ok := s["allOf"].([]any); ok {
		if len(parts) == 1 {
			return g.goType(parts[0].(map[string]any), hint, request)
		}
		merged := map[string]any{"type": "object", "properties": map[string]any{}}
		var req []any
		for _, p := range parts {
			ps := p.(map[string]any)
			if r := refName(ps); r != "" {
				ps = g.schemas[r]
			}
			if props, ok := ps["properties"].(map[string]any); ok {
				for k, v := range props {
					merged["properties"].(map[string]any)[k] = v
				}
			}
			if rq, ok := ps["required"].([]any); ok {
				req = append(req, rq...)
			}
		}
		merged["required"] = req
		return g.objectType(merged, hint, request)
	}
	types, _ := schemaTypes(s)
	typ := ""
	if len(types) > 0 {
		typ = types[0]
	}
	format, _ := s["format"].(string)
	switch typ {
	case "string":
		if format == "binary" {
			return "[]byte"
		}
		return "string"
	case "integer":
		if format == "int64" {
			return "int64"
		}
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		it, _ := s["items"].(map[string]any)
		if it == nil {
			return "[]json.RawMessage"
		}
		return "[]" + g.goType(it, singular(hint), request)
	case "object", "":
		return g.objectType(s, hint, request)
	}
	return "json.RawMessage"
}

func (g *schemaGen) objectType(s map[string]any, hint string, request bool) string {
	props, _ := s["properties"].(map[string]any)
	if len(props) == 0 {
		if ap, ok := s["additionalProperties"].(map[string]any); ok {
			return "map[string]" + g.goType(ap, hint+"Value", request)
		}
		return "map[string]json.RawMessage"
	}
	required := map[string]bool{}
	if rq, ok := s["required"].([]any); ok {
		for _, r := range rq {
			required[r.(string)] = true
		}
	}
	st := &Struct{Name: hint, Doc: cleanDoc(str(s["description"]))}
	fn := uniqueNamer{}
	for _, k := range sortedKeys(props) {
		ps := props[k].(map[string]any)
		ft := g.goType(ps, hint+goName(k), request)
		if request && !required[k] && isScalar(ft) {
			ft = "*" + ft
		}
		doc := cleanDoc(str(ps["description"]))
		if ev := enumValues(ps); ev != "" {
			doc = strings.TrimSpace(doc + " Possible values: " + ev + ".")
		}
		if request && required[k] {
			doc = strings.TrimSpace("Required. " + doc)
		}
		st.Fields = append(st.Fields, Field{Name: fn.name(goName(k)), Type: ft, JSON: k, Doc: doc, Omit: !(request && required[k])})
	}
	return "*" + g.t.add(st)
}

func enumValues(s map[string]any) string {
	if e, ok := s["enum"].([]any); ok {
		var vs []string
		for _, v := range e {
			if v != nil {
				vs = append(vs, fmt.Sprint(v))
			}
		}
		return strings.Join(vs, ", ")
	}
	return ""
}

func isScalar(t string) bool {
	switch t {
	case "string", "int", "int64", "float64", "bool":
		return true
	}
	return false
}

// component returns the Go type name for a named component schema.
func (g *schemaGen) component(name string) string {
	if n, ok := g.done[name]; ok {
		return n
	}
	if g.inflight[name] {
		// Recursive reference: the type name is reserved below.
		return goName(name)
	}
	g.inflight[name] = true
	defer delete(g.inflight, name)
	s := g.schemas[name]
	types, _ := schemaTypes(s)
	_, hasProps := s["properties"]
	_, hasAllOf := s["allOf"]
	if !hasProps && !hasAllOf && (len(types) == 0 || types[0] != "object") {
		t := g.goType(s, goName(name), g.request[name])
		t = strings.TrimPrefix(t, "*")
		g.done[name] = t
		return t
	}
	t := strings.TrimPrefix(g.goType(s, goName(name), g.request[name]), "*")
	g.done[name] = t
	return t
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// mergeSchemas unions component schemas of several specs (first wins,
// properties are unioned).
func mergeSchemas(specs ...*openapi) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, sp := range specs {
		for name, s := range sp.Components.Schemas {
			cur, ok := out[name]
			if !ok {
				out[name] = s
				continue
			}
			cp, _ := cur["properties"].(map[string]any)
			np, _ := s["properties"].(map[string]any)
			for k, v := range np {
				if cp == nil {
					cp = map[string]any{}
					cur["properties"] = cp
				}
				if _, ok := cp[k]; !ok {
					cp[k] = v
				}
			}
		}
	}
	return out
}

var _ = sort.Strings
