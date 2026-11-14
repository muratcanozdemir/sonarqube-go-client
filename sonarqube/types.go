package sonarqube

import (
	"encoding/json"
	"strings"
)

// AnticipatedTransition is one entry of the body of
// [IssuesService.AnticipatedTransitions].
type AnticipatedTransition struct {
	RuleKey      string `json:"ruleKey"`
	IssueMessage string `json:"issueMessage,omitempty"`
	FilePath     string `json:"filePath,omitempty"`
	Line         int    `json:"line,omitempty"`
	LineHash     string `json:"lineHash,omitempty"`
	// Transition is one of falsepositive, wontfix or accept.
	Transition string `json:"transition"`
	Comment    string `json:"comment,omitempty"`
}

// ID is an identifier that SonarQube has published both as a JSON number
// (older versions and documentation examples) and as a string (UUIDs). It
// decodes from either form.
type ID string

// UnmarshalJSON accepts a JSON string, number or null.
func (id *ID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*id = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*id = ID(v)
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*id = ID(n.String())
	}
	return nil
}

// String returns the identifier.
func (id ID) String() string { return string(id) }
