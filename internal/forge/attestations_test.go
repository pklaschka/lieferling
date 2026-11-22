package forge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/snappy"
)

const digest = "sha256:c7b6e765000000000000000000000000000000000000000000000000000fa0eb"

func TestAttestations(t *testing.T) {
	var calls []string
	var answer func(w http.ResponseWriter, r *http.Request)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+" auth="+r.Header.Get("Authorization"))
		answer(w, r)
	}))
	defer srv.Close()
	gh := &GitHub{BaseURL: srv.URL, Token: "secret"}
	ctx := context.Background()

	for _, c := range []struct {
		name   string
		answer func(w http.ResponseWriter, r *http.Request)
		want   []string
		err    bool
	}{
		{"404 is none", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, nil, false},
		{"empty list", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"attestations":[]}`)) }, nil, false},
		{"inline bundle", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("predicate_type") != "https://slsa.dev/provenance/v1" {
				t.Errorf("query %q", r.URL.RawQuery)
			}
			w.Write([]byte(`{"attestations":[{"bundle":{"a":1}},{"bundle":null}]}`))
		}, []string{`{"a":1}`}, false},
		{"bundle_url", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/bundles/") {
				w.Write(snappy.Encode(nil, []byte(`{"b":2}`)))
				return
			}
			w.Write([]byte(`{"attestations":[{"bundle":null,"bundle_url":"` + srv.URL + `/bundles/1"}]}`))
		}, []string{`{"b":2}`}, false},
		{"403 is an error", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusForbidden) }, nil, true},
	} {
		calls, answer = nil, c.answer
		got, err := gh.Attestations(ctx, "o/r", digest)
		if (err != nil) != c.err {
			t.Errorf("%s: err = %v", c.name, err)
		}
		var s []string
		for _, b := range got {
			s = append(s, string(b))
		}
		if strings.Join(s, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: %q, want %q", c.name, s, c.want)
		}
		if !strings.HasPrefix(calls[0], "/repos/o/r/attestations/"+digest+" auth=Bearer secret") {
			t.Errorf("%s: first call %q", c.name, calls[0])
		}
		for _, call := range calls[1:] {
			if !strings.HasSuffix(call, "auth=") {
				t.Errorf("%s: the token went to the bundle host: %q", c.name, call)
			}
		}
	}

	calls = nil
	if _, err := gh.Attestations(ctx, "o/r", "sha256:../../x"); err == nil || len(calls) != 0 {
		t.Errorf("malformed digest: %v, %d calls", err, len(calls))
	}
	client := &Client{GitHub: gh, Gitea: &Gitea{}}
	if _, err := client.Attestations(ctx, "codeberg.org/o/r", digest); !errors.Is(err, ErrNoAttestations) {
		t.Errorf("codeberg: %v", err)
	}
}
