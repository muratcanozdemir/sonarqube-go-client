package sonarqube

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// apiKind selects how parameters are transported.
type apiKind int

const (
	// apiV1 sends parameters in the query string for GET and as a form body
	// otherwise.
	apiV1 apiKind = iota
	// apiV2 sends parameters in the query string and a JSON body.
	apiV2
)

// endpoint is the static description of a generated method's call.
type endpoint struct {
	method      string
	path        string
	kind        apiKind
	accept      string
	contentType string
	multipart   bool
}

// call encodes opts, performs the request and decodes the response into out.
func (c *Client) call(ctx context.Context, ep endpoint, opts any, out any) (*Response, error) {
	enc, err := encodeOptions(opts)
	if err != nil {
		return nil, err
	}
	r := &Request{
		Method:      ep.method,
		Path:        ep.path,
		PathParams:  enc.path,
		Accept:      ep.accept,
		ContentType: ep.contentType,
	}
	r.Header = enc.header
	r.Files = enc.files
	switch {
	case ep.multipart, ep.kind == apiV1 && ep.method != http.MethodGet && enc.body == nil:
		r.Form = enc.values
	default:
		r.Query = enc.values
	}
	switch b := enc.body.(type) {
	case nil:
	case string:
		r.RawBody = []byte(b)
	default:
		r.JSON = b
	}
	return c.Do(ctx, r, out)
}

type encoded struct {
	path   map[string]string
	header http.Header
	values url.Values
	files  map[string]File
	body   any
}

// encodeOptions converts an options struct into request parts using its
// field tags:
//
//	url:"name"        query/form parameter; slices are repeated
//	url:"name,comma"  query/form parameter; slices are comma-joined
//	path:"name"       path parameter
//	header:"name"     request header
//	file:"name"       multipart file (field type *File)
//	body:""           JSON request body
func encodeOptions(opts any) (*encoded, error) {
	e := &encoded{values: url.Values{}}
	if opts == nil {
		return e, nil
	}
	v := reflect.ValueOf(opts)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return e, nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("sonarqube: options must be a struct, got %s", v.Kind())
	}
	t := v.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		switch {
		case f.Tag.Get("path") != "":
			s, ok := scalarString(fv)
			if !ok || s == "" {
				return nil, fmt.Errorf("sonarqube: path parameter %s is required", f.Name)
			}
			if e.path == nil {
				e.path = map[string]string{}
			}
			e.path[f.Tag.Get("path")] = s
		case f.Tag.Get("header") != "":
			if s, ok := scalarString(fv); ok {
				if e.header == nil {
					e.header = http.Header{}
				}
				e.header.Set(f.Tag.Get("header"), s)
			}
		case f.Tag.Get("file") != "":
			if fv.IsNil() {
				continue
			}
			if e.files == nil {
				e.files = map[string]File{}
			}
			e.files[f.Tag.Get("file")] = *fv.Interface().(*File)
		case hasTag(f, "body"):
			switch fv.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
				if fv.IsNil() {
					continue
				}
			case reflect.String:
				if fv.Len() == 0 {
					continue
				}
			}
			e.body = fv.Interface()
		case f.Tag.Get("url") != "":
			name, flag, _ := strings.Cut(f.Tag.Get("url"), ",")
			if fv.Kind() == reflect.Slice {
				if fv.Len() == 0 {
					continue
				}
				items := make([]string, 0, fv.Len())
				for j := range fv.Len() {
					s, _ := scalarString(fv.Index(j))
					items = append(items, s)
				}
				if flag == "comma" {
					e.values.Set(name, strings.Join(items, ","))
				} else {
					e.values[name] = items
				}
				continue
			}
			if s, ok := scalarString(fv); ok {
				e.values.Set(name, s)
			}
		}
	}
	return e, nil
}

func hasTag(f reflect.StructField, key string) bool {
	_, ok := f.Tag.Lookup(key)
	return ok
}

// scalarString formats a scalar or pointer-to-scalar. ok is false for nil
// pointers and empty strings, i.e. unset parameters.
func scalarString(v reflect.Value) (string, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), v.String() != ""
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), true
	case reflect.Int, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64), true
	default:
		return "", false
	}
}

// Ptr returns a pointer to v. It is convenient for optional parameters:
//
//	&sonarqube.IssuesSearchOptions{Ps: sonarqube.Ptr(500)}
func Ptr[T any](v T) *T { return &v }
