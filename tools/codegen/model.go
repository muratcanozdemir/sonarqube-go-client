package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Param is one request parameter of a generated method.
type Param struct {
	Name     string // wire name
	Field    string // Go field name
	GoType   string // Go type expression
	In       string // query, path, header, file
	Comma    bool   // slice is comma-joined (v1)
	Required bool
	Doc      string
}

// Endpoint is one generated method.
type Endpoint struct {
	ServiceType  string // IssuesService
	ServiceField string // Issues
	Method       string // Search
	HTTPMethod   string
	Path         string // with {name} placeholders
	SheetPath    string // as written in the matrix
	API          string // v1 or v2
	Internal     bool
	Deprecated   string
	Since        string
	Doc          string
	Avail        []string // per version column

	Params []*Param

	BodyType        string // Go type of the JSON body ("" = none)
	BodyContentType string
	Multipart       bool

	RespKind string // none, json, bytes
	RespType string // Go type for json
	Accept   string
	Example  string // sample response body for unit tests
}

func (e *Endpoint) OptionsType() string {
	return e.prefix() + "Options"
}

func (e *Endpoint) prefix() string {
	return strings.Replace(e.ServiceType, "Service", "", 1) + e.Method
}

// sheetRow is one row of the endpoint matrix.
type sheetRow struct {
	Path, Method, API string
	ClientMethods     []string
	Avail             []string
}

// sheetParam is one row of the parameter matrix.
type sheetParam struct {
	Path, Method, Name string
	States             []string
}

type sheets struct {
	Columns []string
	Rows    []sheetRow
	Params  map[string][]sheetParam // key: method+" "+path
	Changes map[string]string       // key: method path param -> note
}

func readSheets(matrixFile, paramFile string) (*sheets, error) {
	s := &sheets{Params: map[string][]sheetParam{}, Changes: map[string]string{}}
	f, err := excelize.OpenFile(matrixFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := f.GetRows("Matrix")
	if err != nil {
		return nil, err
	}
	hdrIdx := -1
	for i, r := range rows {
		if len(r) > 0 && r[0] == "Path" {
			hdrIdx = i
			break
		}
	}
	if hdrIdx < 0 {
		return nil, fmt.Errorf("no header in matrix")
	}
	hdr := rows[hdrIdx]
	s.Columns = hdr[6:]
	for _, r := range rows[hdrIdx+1:] {
		if len(r) < 4 || r[0] == "" {
			continue
		}
		row := sheetRow{Path: r[0], Method: r[1], API: r[2]}
		for _, m := range strings.Split(r[3], ",") {
			row.ClientMethods = append(row.ClientMethods, strings.TrimSpace(m))
		}
		row.Avail = make([]string, len(s.Columns))
		for i := range s.Columns {
			if 6+i < len(r) {
				row.Avail[i] = r[6+i]
			}
		}
		s.Rows = append(s.Rows, row)
	}

	pf, err := excelize.OpenFile(paramFile)
	if err != nil {
		return nil, err
	}
	defer pf.Close()
	prow, err := pf.GetRows("Parameter matrix")
	if err != nil {
		return nil, err
	}
	for _, r := range prow {
		if len(r) < 6 || !strings.HasPrefix(r[0], "/") {
			continue
		}
		p := sheetParam{Path: r[0], Method: r[1], Name: r[2]}
		for i := range s.Columns {
			st := ""
			if 6+i < len(r) {
				st = r[6+i]
			}
			p.States = append(p.States, st)
		}
		k := r[1] + " " + r[0]
		s.Params[k] = append(s.Params[k], p)
	}
	crow, err := pf.GetRows("Changes by version")
	if err != nil {
		return nil, err
	}
	for _, r := range crow {
		if len(r) < 8 || !strings.HasPrefix(r[0], "/") || r[5] != "Community" {
			continue
		}
		k := r[1] + " " + r[0] + " " + r[2]
		note := fmt.Sprintf("%s in %s", r[3], strings.TrimSuffix(r[7], " CE"))
		if prev, ok := s.Changes[k]; ok {
			note = prev + "; " + note
		}
		s.Changes[k] = note
	}
	return s, nil
}

// firstAvailable summarises a per-version state slice as a since/until note.
func availabilityNote(cols, states []string, present func(string) bool) string {
	var ce []string
	for i, c := range cols {
		if strings.HasSuffix(c, " CE") && present(states[i]) {
			ce = append(ce, strings.TrimSuffix(c, " CE"))
		}
	}
	nCE := 0
	for _, c := range cols {
		if strings.HasSuffix(c, " CE") {
			nCE++
		}
	}
	switch {
	case len(ce) == 0:
		return "Not available in Community Edition."
	case len(ce) == nCE:
		return ""
	default:
		return fmt.Sprintf("Community Edition: %s.", compressRanges(cols, ce))
	}
}

// compressRanges renders a list of CE versions as contiguous ranges.
func compressRanges(cols, versions []string) string {
	var order []string
	for _, c := range cols {
		if strings.HasSuffix(c, " CE") {
			order = append(order, strings.TrimSuffix(c, " CE"))
		}
	}
	in := map[string]bool{}
	for _, v := range versions {
		in[v] = true
	}
	var parts []string
	for i := 0; i < len(order); i++ {
		if !in[order[i]] {
			continue
		}
		j := i
		for j+1 < len(order) && in[order[j+1]] {
			j++
		}
		if i == j {
			parts = append(parts, order[i])
		} else {
			parts = append(parts, order[i]+"–"+order[j])
		}
		i = j
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
