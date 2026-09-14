package desktop

import "testing"

func TestWindowURLRejectsExternalAndExecutableTargets(t *testing.T) {
	for _, target := range []string{"", "--utility-cmd-prefix=malicious", "file:///tmp/page.html", "javascript:alert(1)", "https://example.com", "http://127.0.0.1@example.com", "http://localhost.example.com", "http://user@localhost:1738"} {
		if err := validateWindowURL(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
	for _, target := range []string{"http://127.0.0.1:1738", "http://localhost:1738/chat", "http://[::1]:1738"} {
		if err := validateWindowURL(target); err != nil {
			t.Errorf("rejected %q: %v", target, err)
		}
	}
}
