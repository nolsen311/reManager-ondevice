//go:build linux

package ondevice

import (
	"strings"
	"testing"
)

func TestWithOfflineHint(t *testing.T) {
	offline := []string{
		`could not load the package catalogue: failed to fetch packages metadata: Get "https://packages.vellum.delivery/packages-metadata.json": dial tcp: lookup packages.vellum.delivery: no such host`,
		"Installation failed: WARNING: fetching https://packages.vellum.delivery/aarch64/APKINDEX.tar.gz: DNS lookup error",
		"dial tcp 1.2.3.4:443: connect: network is unreachable",
	}
	for _, message := range offline {
		got := withOfflineHint(message)
		if !strings.HasPrefix(got, OfflineMessage) || !strings.HasSuffix(got, message) {
			t.Errorf("withOfflineHint(%q) = %q", message, got)
		}
		if again := withOfflineHint(got); again != got {
			t.Errorf("the hint should be added once, got %q", again)
		}
	}
	for _, message := range []string{"", "koreader is not installed.", "ERROR: unable to select packages:\n  foo (no such package):"} {
		if got := withOfflineHint(message); got != message {
			t.Errorf("withOfflineHint(%q) = %q, want it unchanged", message, got)
		}
	}
}

func TestOfflineHintReachesTheFrontend(t *testing.T) {
	catalog := &fakeCatalog{refreshErr: errorString("dial tcp: lookup packages.vellum.delivery: no such host")}
	h := &Handler{Service: newTestService(catalog, standardTablet()), Jobs: &Jobs{Dir: t.TempDir()}}
	f := serve(t, h)

	resp := f.request(t, `{"id":1,"method":"list"}`)
	if message, _ := resp["error"].(string); !strings.HasPrefix(message, OfflineMessage) {
		t.Errorf("error = %q", message)
	}
}
