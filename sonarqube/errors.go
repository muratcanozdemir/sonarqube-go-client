package sonarqube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorResponse is returned for any non-2xx API response.
type ErrorResponse struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Method and URL identify the failed request.
	Method string
	URL    string
	// Messages are the error messages reported by SonarQube, from
	// v1 {"errors":[{"msg":...}]} or v2 {"message":...} bodies.
	Messages []string
	// Body is the raw response body.
	Body []byte
}

func newErrorResponse(r *Response) *ErrorResponse {
	e := &ErrorResponse{StatusCode: r.StatusCode, Body: r.Body}
	if r.Request != nil {
		e.Method = r.Request.Method
		e.URL = r.Request.URL.Redacted()
	}
	var v1 struct {
		Errors []struct {
			Msg string `json:"msg"`
		} `json:"errors"`
		Message string `json:"message"`
	}
	if json.Unmarshal(r.Body, &v1) == nil {
		for _, m := range v1.Errors {
			e.Messages = append(e.Messages, m.Msg)
		}
		if v1.Message != "" {
			e.Messages = append(e.Messages, v1.Message)
		}
	}
	return e
}

func (e *ErrorResponse) Error() string {
	msg := strings.Join(e.Messages, "; ")
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("sonarqube: %s %s: %d: %s", e.Method, e.URL, e.StatusCode, msg)
}

// StatusCode returns the HTTP status code carried by err, or 0 if err is not
// an *ErrorResponse.
func StatusCode(err error) int {
	var e *ErrorResponse
	if errors.As(err, &e) {
		return e.StatusCode
	}
	return 0
}

// IsNotFound reports whether err is a 404 API error.
func IsNotFound(err error) bool { return StatusCode(err) == http.StatusNotFound }

// IsUnauthorized reports whether err is a 401 API error.
func IsUnauthorized(err error) bool { return StatusCode(err) == http.StatusUnauthorized }

// IsForbidden reports whether err is a 403 API error.
func IsForbidden(err error) bool { return StatusCode(err) == http.StatusForbidden }
