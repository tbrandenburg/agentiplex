package core

import "testing"

func TestDeliveryStateClassification(t *testing.T) {
	for _, tc := range []struct {
		state string
		terminal, pending, aggregate bool
	}{
		{"preparing", false, true, false},
		{"queued", false, true, false},
		{"attempting", false, true, false},
		{"accepted", false, true, false},
		{"fanout", false, true, false},
		{"broadcast-admitted", false, false, false},
		{"completed", true, false, true},
		{"failed", true, false, true},
		{"uncertain", true, false, true},
		{"input-required", true, false, true},
		{"auth-required", true, false, true},
		{"partial", false, false, true},
		{"", false, false, false},
		{"unknown", false, false, false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			if terminalState(tc.state) != tc.terminal || pendingState(tc.state) != tc.pending || terminalOrPartial(tc.state) != tc.aggregate {
				t.Fatalf("unexpected classification for %q", tc.state)
			}
		})
	}
}

func TestScopedDeliveryIDCompatibility(t *testing.T) {
	for _, tc := range []struct {
		prefix, caller, key, want string
	}{
		{"a2a-", "pm", "request-1", "a2a-7dad90a214984ec5fab5a70cc441f200c2940e7b0e7fe9adeda536706abd"},
		{"a2a-", "", "request-1", "a2a-b51a05f1db47620a3da6d2750e55363aa3db560206f51944f74d93ea50c3"},
		{"bg-", "pm", "request-1", "bg-7dad90a214984ec5fab5a70cc441f200c2940e7b0e7fe9adeda536706abd"},
		{"dp-", "pm", "request-1", "dp-7dad90a214984ec5fab5a70cc441f200c2940e7b0e7fe9adeda536706abd"},
		{"bc-", "", "parent\x00worker", "bc-d9ff76e871d455a5106c6dd6ac5c4fb3bdc4cf63039c6467b04bda980a91"},
	} {
		if got := scopedID(tc.prefix, tc.caller, tc.key); got != tc.want {
			t.Errorf("scopedID(%q, %q, %q) = %q, want %q", tc.prefix, tc.caller, tc.key, got, tc.want)
		}
	}
}
