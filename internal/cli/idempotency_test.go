package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestSubmissionKeys(t *testing.T) {
	type request struct {
		Path string
		Key  string
	}
	received := make(chan request, 16)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key string `json:"idempotencyKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		received <- request{r.URL.Path, in.Key}
		fmt.Fprint(w, `{"id":"receipt","state":"queued"}`)
	}))
	defer s.Close()
	seen := map[string]bool{}
	for _, tc := range []struct {
		command, path string
	}{
		{"send", "/api/v1/messages"},
		{"broadcast", "/api/v1/broadcasts"},
		{"dispatch", "/api/v1/dispatches"},
	} {
		for i := 0; i < 2; i++ {
			out, diag, err := runObservationCLI(t, s.URL, tc.command, "team", "same text")
			if err != nil {
				t.Fatal(err)
			}
			got := <-received
			if got.Path != tc.path || !regexp.MustCompile(`^cli-[0-9a-f]{32}$`).MatchString(got.Key) || seen[got.Key] {
				t.Fatalf("unexpected or repeated submission: %+v", got)
			}
			seen[got.Key] = true
			if diag != "Idempotency key: "+got.Key+"\n" || !json.Valid([]byte(out)) {
				t.Fatalf("stdout/stderr changed: %q %q", out, diag)
			}
		}
		for i := 0; i < 2; i++ {
			out, diag, err := runObservationCLI(t, s.URL, tc.command, "team", "same text", "--idempotency-key", "stable")
			if err != nil {
				t.Fatal(err)
			}
			if got := <-received; got.Key != "stable" || got.Path != tc.path || diag != "" || !json.Valid([]byte(out)) {
				t.Fatalf("explicit key changed: %+v %q %q", got, out, diag)
			}
		}
		if _, _, err := runObservationCLI(t, s.URL, tc.command, "team", "same text", "--idempotency-key", ""); err == nil {
			t.Fatal("explicit empty key accepted")
		}
		select {
		case got := <-received:
			t.Fatalf("invalid key reached server: %+v", got)
		default:
		}
	}
}

func TestSubmissionFailureReportsRetryKey(t *testing.T) {
	keys := make(chan string, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key string `json:"idempotencyKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		keys <- in.Key
		http.Error(w, "response unavailable", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	_, diag, err := runObservationCLI(t, s.URL, "send", "lead", "topic", "--until-done", "--wait")
	if err == nil {
		t.Fatal("submission unexpectedly succeeded")
	}
	key := <-keys
	if key == "" || !strings.Contains(diag, "Idempotency key: "+key) || !strings.Contains(err.Error(), "--idempotency-key "+fmt.Sprintf("%q", key)) {
		t.Fatalf("retry key missing: %q %q %v", key, diag, err)
	}
	_, _, err = runObservationCLI(t, s.URL, "send", "lead", "topic", "--until-done", "--wait", "--idempotency-key", key)
	if err == nil || <-keys != key {
		t.Fatal("retry did not preserve key")
	}
}
