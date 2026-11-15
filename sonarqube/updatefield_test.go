package sonarqube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// SonarQube's v2 PATCH endpoints read an UpdateField as its bare value; the
// documented {"defined","value"} object is refused as "Invalid request format".
func TestUpdateFieldJSON(t *testing.T) {
	for name, c := range map[string]struct {
		body UserUpdateRestRequest
		want string
	}{
		"unset field is omitted": {UserUpdateRestRequest{}, `{}`},
		"value":                  {UserUpdateRestRequest{SCMAccounts: &UpdateFieldListString{Value: []string{"a", "b"}}}, `{"scmAccounts":["a","b"]}`},
		"nil list clears":        {UserUpdateRestRequest{SCMAccounts: &UpdateFieldListString{}}, `{"scmAccounts":[]}`},
	} {
		got, err := json.Marshal(c.body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}

	var u UpdateFieldListString
	if err := json.Unmarshal([]byte(`["x"]`), &u); err != nil {
		t.Fatal(err)
	}
	if u.Defined == nil || !*u.Defined || !reflect.DeepEqual(u.Value, []string{"x"}) {
		t.Errorf("decoded %+v", u)
	}
	if err := json.Unmarshal([]byte(`{"defined":true,"value":["x"]}`), &u); err == nil {
		t.Error("decoding an object: want an error")
	}
}

func TestUpdateFieldOnTheWire(t *testing.T) {
	var gotBody, gotType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotType = string(b), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u1","login":"jdoe","scmAccounts":["a"]}`))
	})
	_, _, err := c.UsersManagement.Update(context.Background(), &UsersManagementUpdateOptions{
		ID: "u1", Body: &UserUpdateRestRequest{SCMAccounts: &UpdateFieldListString{Value: []string{"a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"scmAccounts":["a"]}` {
		t.Errorf("body %s", gotBody)
	}
	if gotType != "application/merge-patch+json" {
		t.Errorf("content type %s", gotType)
	}
}
