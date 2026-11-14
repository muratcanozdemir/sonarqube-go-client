package sonarqube

import "strings"

// Paging is the v1 pagination object.
type Paging struct {
	PageIndex int64 `json:"pageIndex,omitempty"`
	PageSize  int64 `json:"pageSize,omitempty"`
	Total     int64 `json:"total,omitempty"`
}

// Availability describes whether an endpoint exists on a SonarQube release.
type Availability byte

const (
	// Unavailable means the endpoint does not exist on the release.
	Unavailable Availability = '-'
	// Public means the endpoint exists and is public.
	Public Availability = 'Y'
	// Internal means the endpoint exists but SonarQube marks it internal.
	Internal Availability = 'I'
	// OtherMethod means the path exists but with a different HTTP method.
	OtherMethod Availability = 'M'
)

// Exists reports whether the endpoint can be called on the release.
func (a Availability) Exists() bool { return a == Public || a == Internal }

// ParamInfo describes one parameter of an endpoint.
type ParamInfo struct {
	Name     string
	In       string // query, path, header, file or body
	Required bool
	Multi    bool
}

// EndpointInfo describes one API endpoint wrapped by this client.
type EndpointInfo struct {
	Service    string // client field, e.g. "Issues"
	Method     string // method name, e.g. "Search"
	HTTPMethod string
	Path       string
	API        string // "v1" or "v2"
	Internal   bool
	Deprecated bool
	// ContentType is the request body media type when it is not
	// application/json, e.g. "application/merge-patch+json".
	ContentType string
	// Multipart reports that parameters travel as multipart/form-data.
	Multipart bool
	Params    []ParamInfo

	availability string
}

// Endpoints returns metadata for every endpoint wrapped by the client.
func Endpoints() []EndpointInfo {
	out := make([]EndpointInfo, len(endpointRegistry))
	copy(out, endpointRegistry)
	return out
}

// Releases returns the SonarQube releases the availability matrix covers,
// e.g. "25.1 CE" or "2026.5.5 EE".
func Releases() []string {
	return append([]string(nil), versionColumns...)
}

// AvailableOn reports the endpoint's availability on a release label as
// returned by [Releases]. Unknown releases report Unavailable.
func (e EndpointInfo) AvailableOn(release string) Availability {
	for i, c := range versionColumns {
		if c == release && i < len(e.availability) {
			return Availability(e.availability[i])
		}
	}
	return Unavailable
}

// ReleaseLabel maps a server version string, as returned by
// api/server/version (e.g. "26.11.0.134175" or "2026.5.5.134787"), and an
// edition ("community", "enterprise", ...) to the matching label of
// [Releases]. It returns "" when no tested release matches.
func ReleaseLabel(serverVersion, edition string) string {
	parts := strings.Split(serverVersion, ".")
	suffix := " CE"
	if !strings.EqualFold(edition, "community") && edition != "" {
		suffix = " EE"
	}
	var cand string
	if suffix == " CE" && len(parts) >= 2 {
		cand = parts[0] + "." + parts[1] + suffix
	} else if len(parts) >= 3 {
		cand = parts[0] + "." + parts[1] + "." + parts[2] + suffix
	}
	for _, c := range versionColumns {
		if c == cand {
			return c
		}
	}
	return ""
}
