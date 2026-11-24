package a2a

import "testing"

func TestPublicURL(t *testing.T) {
	for _, test := range []struct {
		configured, listen, want string
	}{
		{"", "127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"", "[::1]:8123", "http://[::1]:8123"},
		{"https://agentiplex.example/", "0.0.0.0:8080", "https://agentiplex.example"},
		{"", "0.0.0.0:8080", ""},
		{"http://0.0.0.0:8080", "0.0.0.0:8080", ""},
		{"http://example.com/a2a", "127.0.0.1:8080", ""},
		{"http://user:secret@example.com", "127.0.0.1:8080", ""},
	} {
		got, err := PublicURL(test.configured, test.listen)
		if test.want == "" {
			if err == nil {
				t.Errorf("unexpected valid URL %q", got)
			}
		} else if err != nil || got != test.want {
			t.Errorf("PublicURL(%q,%q) = %q, %v; want %q", test.configured, test.listen, got, err, test.want)
		}
	}
}
