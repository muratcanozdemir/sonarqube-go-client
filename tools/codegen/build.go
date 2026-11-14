package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// v1 metadata from /api/webservices/list.
type wsList struct {
	WebServices []struct {
		Path    string `json:"path"`
		Actions []*wsAction
	} `json:"webServices"`
}

type wsAction struct {
	Key                string `json:"key"`
	Description        string `json:"description"`
	Since              string `json:"since"`
	DeprecatedSince    string `json:"deprecatedSince"`
	Internal           bool   `json:"internal"`
	Post               bool   `json:"post"`
	HasResponseExample bool   `json:"hasResponseExample"`
	ContentType        string `json:"contentType"`
	Params             []*wsParam
}

type wsParam struct {
	Key              string   `json:"key"`
	Description      string   `json:"description"`
	Required         bool     `json:"required"`
	Internal         bool     `json:"internal"`
	Since            string   `json:"since"`
	DeprecatedSince  string   `json:"deprecatedSince"`
	DefaultValue     string   `json:"defaultValue"`
	ExampleValue     string   `json:"exampleValue"`
	PossibleValues   []string `json:"possibleValues"`
	MaximumValue     *int     `json:"maximumValue"`
	MaxValuesAllowed *int     `json:"maxValuesAllowed"`
	DeprecatedKey    string   `json:"deprecatedKey"`
}

type example struct {
	Format  string `json:"format"`
	Example string `json:"example"`
}

type harvest struct {
	name     string
	actions  map[string]*wsAction // "/api/issues/search"
	examples map[string]example   // "/api/issues/search"
	spec     *openapi
}

func loadHarvest(dir string) (*harvest, error) {
	h := &harvest{name: filepath.Base(dir), actions: map[string]*wsAction{}, examples: map[string]example{}}
	var ws wsList
	if err := readJSON(filepath.Join(dir, "webservices.json"), &ws); err != nil {
		return nil, err
	}
	for _, w := range ws.WebServices {
		for _, a := range w.Actions {
			h.actions["/"+w.Path+"/"+a.Key] = a
		}
	}
	ex := map[string]example{}
	if err := readJSON(filepath.Join(dir, "examples.json"), &ex); err != nil {
		return nil, err
	}
	for k, v := range ex {
		h.examples["/"+k] = v
	}
	h.spec = &openapi{}
	if err := readJSON(filepath.Join(dir, "api-docs.json"), h.spec); err != nil {
		return nil, err
	}
	return h, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var braceRE = regexp.MustCompile(`\{[^}]*\}`)

// fileParams are v1 parameters uploaded as multipart files.
var fileParams = map[string]bool{
	"/api/qualityprofiles/restore backup": true,
	"/api/ce/submit report":               true,
}

// v1PathOverrides fixes matrix paths that are not served under /api.
var v1PathOverrides = map[string]string{
	"/api/batch/file":    "/batch/file",
	"/api/batch/index":   "/batch/index",
	"/api/batch/project": "/batch/project",
}

// v1ExampleOverrides supplies response examples for endpoints that do not
// publish one in /api/webservices/response_example.
var v1ExampleOverrides = map[string]example{
	"/batch/index":   {Format: "txt"},
	"/batch/file":    {Format: "bin"},
	"/batch/project": {Format: "proto"},
}

// v1JSONBodies lists v1 endpoints that read a JSON request body. Their
// parameters travel in the query string. The body types are hand-written in
// the library (types.go).
var v1JSONBodies = map[string]string{
	"/api/issues/anticipated_transitions": "[]*AnticipatedTransition",
}

type builder struct {
	sheets        *sheets
	harvests      []*harvest // CE first
	types         *Types
	sg            *schemaGen
	endpoints     []*Endpoint
	warnings      []string
	typeOverrides map[string]string
}

func (b *builder) warn(f string, a ...any) { b.warnings = append(b.warnings, fmt.Sprintf(f, a...)) }

func (b *builder) build() error {
	b.sg = &schemaGen{t: b.types, done: map[string]string{}, request: map[string]bool{}, inflight: map[string]bool{}}
	var specs []*openapi
	for _, h := range b.harvests {
		specs = append(specs, h.spec)
	}
	b.sg.schemas = mergeSchemas(specs...)
	// Mark request schemas first so pointer style is consistent.
	for _, h := range b.harvests {
		for _, ops := range h.spec.Paths {
			for _, op := range ops {
				if op.RequestBody != nil {
					for _, c := range op.RequestBody.Content {
						b.sg.markRequest(c.Schema)
					}
				}
			}
		}
	}
	for _, row := range b.sheets.Rows {
		for i, cm := range row.ClientMethods {
			svc, meth, _ := strings.Cut(cm, ".")
			e := &Endpoint{
				ServiceType:  svc,
				ServiceField: strings.Replace(svc, "Service", "", 1),
				Method:       strings.ToUpper(meth[:1]) + meth[1:],
				HTTPMethod:   row.Method,
				SheetPath:    row.Path,
				API:          row.API,
				Avail:        row.Avail,
			}
			var err error
			if row.API == "v1" {
				err = b.buildV1(e, row)
			} else {
				err = b.buildV2(e, row, i, len(row.ClientMethods))
			}
			if err != nil {
				return fmt.Errorf("%s: %w", cm, err)
			}
			b.endpoints = append(b.endpoints, e)
		}
	}
	return nil
}

func (b *builder) lookupV1(path string) (*wsAction, example, bool) {
	var act *wsAction
	var ex example
	found := false
	for _, h := range b.harvests {
		if a, ok := h.actions[path]; ok && act == nil {
			act, found = a, true
		}
		if e, ok := h.examples[path]; ok && ex.Format == "" {
			ex = e
		}
	}
	return act, ex, found
}

func (b *builder) buildV1(e *Endpoint, row sheetRow) error {
	e.Path = row.Path
	if p, ok := v1PathOverrides[row.Path]; ok {
		e.Path = p
	}
	act, ex, found := b.lookupV1(row.Path)
	if o, ok := v1ExampleOverrides[e.Path]; ok {
		ex = o
	}
	if found {
		e.Doc = cleanDoc(act.Description)
		e.Internal = act.Internal
		e.Deprecated = act.DeprecatedSince
		e.Since = act.Since
	} else {
		b.warn("v1 %s %s: no metadata in harvest", row.Method, row.Path)
	}
	meta := map[string]*wsParam{}
	if act != nil {
		for _, p := range act.Params {
			meta[p.Key] = p
		}
	}
	names := uniqueNamer{}
	seen := map[string]bool{}
	add := func(name string, sp *sheetParam) {
		if seen[name] {
			return
		}
		seen[name] = true
		p := &Param{Name: name, In: "query", GoType: "string"}
		m := meta[name]
		var doc []string
		if m != nil {
			p.Required = m.Required
			p.GoType, p.Comma = v1ParamType(m)
			if m.Required {
				doc = append(doc, "Required.")
			}
			if d := cleanDoc(m.Description); d != "" {
				doc = append(doc, d)
			}
			if len(m.PossibleValues) > 0 && p.GoType != "*bool" {
				doc = append(doc, "Possible values: "+strings.Join(m.PossibleValues, ", ")+".")
			}
			if m.DefaultValue != "" {
				doc = append(doc, "Default: "+m.DefaultValue+".")
			}
			if m.ExampleValue != "" {
				doc = append(doc, "Example: "+cleanDoc(m.ExampleValue)+".")
			}
			if m.DeprecatedSince != "" {
				doc = append(doc, "Deprecated: since "+m.DeprecatedSince+".")
			}
			if m.Internal {
				doc = append(doc, "Internal parameter.")
			}
		} else if found {
			doc = append(doc, "Not declared by the latest harvested SonarQube versions.")
		}
		if sp != nil {
			if n := availabilityNote(b.sheets.Columns, sp.States, func(s string) bool { return s != "" && s != "—" }); n != "" {
				doc = append(doc, n)
			}
			if c, ok := b.sheets.Changes[row.Method+" "+row.Path+" "+name]; ok {
				doc = append(doc, "Changes: "+c+".")
			}
		}
		if fileParams[row.Path+" "+name] {
			p.In, p.GoType = "file", "*File"
		}
		p.Field = names.name(goName(name))
		p.Doc = strings.Join(doc, " ")
		e.Params = append(e.Params, p)
	}
	for _, sp := range b.sheets.Params[row.Method+" "+row.Path] {
		sp := sp
		add(sp.Name, &sp)
	}
	if act != nil {
		for _, p := range act.Params {
			add(p.Key, nil)
		}
	}

	if bt, ok := v1JSONBodies[row.Path]; ok {
		e.BodyType = bt
	}

	// Response.
	switch {
	case act != nil && act.ContentType == "NO_CONTENT":
		e.RespKind = "none"
	case ex.Format == "json":
		v, err := decodeExample(ex.Example)
		if err != nil {
			b.warn("v1 %s: bad example: %v", row.Path, err)
			e.RespKind = "rawjson"
			break
		}
		t := b.types.infer(e.prefix()+"Response", v, b.typeOverrides)
		e.RespKind, e.RespType, e.Example = "json", strings.TrimPrefix(t, "*"), ex.Example
		for k := range b.typeOverrides {
			if strings.HasPrefix(k, e.prefix()+"Response") {
				// The published example disagrees with the real response.
				e.Example = "{}"
			}
		}
		if !strings.HasPrefix(t, "*") {
			e.RespType = t
			e.RespKind = "jsonval"
		}
	case ex.Format != "" || (act != nil && act.ContentType == "BINARY"):
		e.RespKind, e.Accept = "bytes", "*/*"
		e.Example = ex.Example
	case row.Method == "GET":
		e.RespKind = "rawjson"
	default:
		e.RespKind = "none"
	}
	return nil
}

func v1ParamType(m *wsParam) (string, bool) {
	pv := map[string]bool{}
	for _, v := range m.PossibleValues {
		pv[v] = true
	}
	if len(pv) > 0 && len(pv) <= 4 && (pv["true"] || pv["yes"]) && (pv["false"] || pv["no"]) {
		allBool := true
		for v := range pv {
			allBool = allBool && (v == "true" || v == "false" || v == "yes" || v == "no")
		}
		if allBool {
			return "*bool", false
		}
	}
	desc := strings.ToLower(m.Description)
	if strings.Contains(desc, "once for each") {
		// Multi-value parameter repeated in the request, e.g. settings/set values.
		return "[]string", false
	}
	ex := strings.TrimSpace(m.ExampleValue)
	if strings.Contains(ex, ",") && !strings.Contains(ex, " ") {
		return "[]string", true
	}
	if m.MaxValuesAllowed != nil || strings.Contains(desc, "comma-separated") || strings.Contains(desc, "comma separated") || strings.Contains(desc, "list of") && strings.Contains(desc, "comma") {
		return "[]string", true
	}
	if m.MaximumValue != nil || m.Key == "p" || m.Key == "ps" || m.Key == "pageSize" || m.Key == "pageIndex" {
		return "*int", false
	}
	return "string", false
}

// findOp finds the spec operation for a matrix path like
// /api/v2/users-management/users/{}.
func (b *builder) findOp(sheetPath, method string) (string, *operation, *harvest) {
	want := strings.TrimPrefix(sheetPath, "/api/v2")
	cands := []string{want}
	if strings.Contains(want, "{}-") {
		cands = append(cands, strings.Replace(want, "{}-", "github-", 1))
	}
	for _, h := range b.harvests {
		for p, ops := range h.spec.Paths {
			norm := braceRE.ReplaceAllString(p, "{}")
			for _, c := range cands {
				if norm == c {
					if op, ok := ops[strings.ToLower(method)]; ok {
						return p, op, h
					}
				}
			}
		}
	}
	return "", nil, nil
}

func (b *builder) buildV2(e *Endpoint, row sheetRow, idx, n int) error {
	specPath, op, _ := b.findOp(row.Path, row.Method)
	names := uniqueNamer{"Body": true}
	if op == nil {
		b.warn("v2 %s %s: no metadata in harvest", row.Method, row.Path)
		// Fall back to matrix information.
		e.Path = row.Path
		for i := 0; strings.Contains(e.Path, "{}"); i++ {
			e.Path = strings.Replace(e.Path, "{}", fmt.Sprintf("{p%d}", i), 1)
			e.Params = append(e.Params, &Param{Name: fmt.Sprintf("p%d", i), Field: names.name(fmt.Sprintf("P%d", i)), GoType: "string", In: "path", Required: true, Doc: "Required. Path parameter."})
		}
		for _, sp := range b.sheets.Params[row.Method+" "+row.Path] {
			doc := "Not declared by the latest harvested SonarQube versions."
			e.Params = append(e.Params, &Param{Name: sp.Name, Field: names.name(goName(sp.Name)), GoType: "string", In: "query", Doc: doc})
		}
		e.RespKind = "rawjson"
		if row.Method != "GET" {
			e.RespKind = "rawjsonopt"
		}
		e.Internal = true
		return nil
	}
	e.Path = "/api/v2" + specPath
	if strings.Contains(row.Path, "{}-") {
		e.Path = "/api/v2" + strings.Replace(specPath, "github-", "{devOpsPlatform}-", 1)
		e.Params = append(e.Params, &Param{Name: "devOpsPlatform", Field: names.name("DevOpsPlatform"), GoType: "string", In: "path", Required: true, Doc: "Required. DevOps platform: github or gitlab."})
	}
	e.Doc = cleanDoc(strings.TrimSpace(op.Summary + ". " + op.Description))
	e.Doc = strings.TrimPrefix(strings.ReplaceAll(e.Doc, "..", "."), ". ")
	e.Internal = op.Internal == true || op.Internal == "true"
	if op.Deprecated {
		e.Deprecated = "unknown"
	}
	seen := map[string]bool{}
	for _, p := range op.Parameters {
		if p.In == "header" && (p.Name == "Content-Length" || p.Name == "X-Sonar-Passcode") {
			continue
		}
		seen[p.Name] = true
		gt := b.sg.goType(p.Schema, e.prefix()+goName(p.Name), true)
		comma := false
		if p.In == "path" {
			gt = "string"
		} else if isScalar(gt) {
			gt = "*" + gt
		}
		if gt == "*string" || p.In == "path" {
			gt = "string"
		}
		var doc []string
		if p.Required {
			doc = append(doc, "Required.")
		}
		if d := cleanDoc(p.Description); d != "" {
			doc = append(doc, d)
		}
		if ev := enumValues(p.Schema); ev != "" {
			doc = append(doc, "Possible values: "+ev+".")
		} else if it, ok := p.Schema["items"].(map[string]any); ok {
			if ev := enumValues(it); ev != "" {
				doc = append(doc, "Possible values: "+ev+".")
			}
		}
		if p.Deprecated {
			doc = append(doc, "Deprecated.")
		}
		if c, ok := b.sheets.Changes[row.Method+" "+row.Path+" "+p.Name]; ok {
			doc = append(doc, "Changes: "+c+".")
		}
		for _, sp := range b.sheets.Params[row.Method+" "+row.Path] {
			if sp.Name == p.Name {
				if n := availabilityNote(b.sheets.Columns, sp.States, func(s string) bool { return s != "" && s != "—" }); n != "" {
					doc = append(doc, n)
				}
			}
		}
		e.Params = append(e.Params, &Param{Name: p.Name, Field: names.name(goName(p.Name)), GoType: gt, In: p.In, Comma: comma, Required: p.Required, Doc: strings.Join(doc, " ")})
	}
	for _, m := range braceRE.FindAllString(e.Path, -1) {
		name := strings.Trim(m, "{}")
		if !seen[name] {
			seen[name] = true
			e.Params = append(e.Params, &Param{Name: name, Field: names.name(goName(name)), GoType: "string", In: "path", Required: true, Doc: "Required. Path parameter."})
		}
	}
	for _, sp := range b.sheets.Params[row.Method+" "+row.Path] {
		if seen[sp.Name] {
			continue
		}
		seen[sp.Name] = true
		doc := "Not declared by the latest harvested SonarQube versions."
		if n := availabilityNote(b.sheets.Columns, sp.States, func(s string) bool { return s != "" && s != "—" }); n != "" {
			doc += " " + n
		}
		if c, ok := b.sheets.Changes[row.Method+" "+row.Path+" "+sp.Name]; ok {
			doc += " Changes: " + c + "."
		}
		e.Params = append(e.Params, &Param{Name: sp.Name, Field: names.name(goName(sp.Name)), GoType: "string", In: "query", Doc: doc})
	}

	// Request body.
	if op.RequestBody != nil {
		for _, ct := range []string{"application/json", "application/merge-patch+json", "multipart/form-data", "application/x-www-form-urlencoded"} {
			c, ok := op.RequestBody.Content[ct]
			if !ok {
				continue
			}
			switch ct {
			case "multipart/form-data":
				e.Multipart = true
				s := c.Schema
				if r := refName(s); r != "" {
					s = b.sg.schemas[r]
				}
				props, _ := s["properties"].(map[string]any)
				for _, k := range sortedKeys(props) {
					ps := props[k].(map[string]any)
					gt := b.sg.goType(ps, e.prefix()+goName(k), true)
					in := "query"
					if gt == "[]byte" {
						gt, in = "*File", "file"
					} else if gt != "string" && isScalar(gt) {
						gt = "*" + gt
					}
					e.Params = append(e.Params, &Param{Name: k, Field: names.name(goName(k)), GoType: gt, In: in, Doc: cleanDoc(str(ps["description"]))})
				}
			default:
				gt := b.sg.goType(c.Schema, e.prefix()+"Body", true)
				e.BodyType = gt
				if ct != "application/json" {
					e.BodyContentType = ct
				}
			}
			break
		}
	}

	// Response.
	e.RespKind = "none"
	for _, code := range []string{"200", "201", "202", "204"} {
		r, ok := op.Responses[code]
		if !ok {
			continue
		}
		if len(r.Content) == 0 {
			break
		}
		jc, hasJSON := r.Content["application/json"]
		if len(r.Content) > 1 || !hasJSON {
			e.RespKind = "bytes"
			e.Accept = "*/*"
			if len(r.Content) > 1 {
				e.Params = append(e.Params, &Param{Name: "Accept", Field: names.name("Accept"), GoType: "string", In: "header",
					Doc: "Response media type. One of: " + strings.Join(sortedKeys(r.Content), ", ") + "."})
			}
			break
		}
		gt := b.sg.goType(jc.Schema, e.prefix()+"Response", false)
		switch {
		case gt == "[]byte":
			e.RespKind, e.Accept = "bytes", "*/*"
		case strings.HasPrefix(gt, "*"):
			e.RespKind, e.RespType = "json", gt[1:]
		default:
			e.RespKind, e.RespType = "jsonval", gt
		}
		break
	}
	// Two client methods on one row: download (binary) + metadata (JSON).
	if n == 2 {
		kept := e.Params[:0]
		for _, p := range e.Params {
			if p.In != "header" || p.Name != "Accept" {
				kept = append(kept, p)
			}
		}
		e.Params = kept
		if idx == 0 {
			e.RespKind, e.Accept, e.RespType = "bytes", "application/octet-stream", ""
		} else {
			e.RespKind, e.Accept, e.RespType = "rawjson", "application/json", ""
		}
	}
	return nil
}
