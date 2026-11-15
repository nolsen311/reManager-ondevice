package ondevice

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// hashtabHex is the start of a hashtab as hexdump prints it: an empty first entry, then the version entry qt-resource-rebuilder writes.
func hashtabHex(version string) string {
	var b []byte
	b = binary.BigEndian.AppendUint64(b, 0)
	b = binary.BigEndian.AppendUint32(b, 0)
	b = binary.BigEndian.AppendUint64(b, 17607111715072197239)
	b = binary.BigEndian.AppendUint32(b, uint32(len(version)))
	b = append(b, version...)
	return hex.EncodeToString(b)
}

const coreInstalled = "vellum-1.0.0-r0 aarch64 {vellum} (MIT) [installed]\nremarkable-os-3.27.3.0-r0 aarch64 {remarkable-os} (MIT) [installed]\n"

func healthyTablet() *fakeTablet {
	return (&fakeTablet{}).
		on("RELEASE_VERSION", "RELEASE_VERSION=3.27.3.0\n", nil).
		on("state/osver", "3.27.3.0\n", nil).
		on("list -I", coreInstalled+"tripletap-1.1.1-r0 aarch64 {tripletap} (MIT) [installed]\n", nil).
		on("info -q", "vellum\nremarkable-os\ntripletap\n", nil).
		on("reenable status", "ok\n", nil).
		on("test -x '/home/root/xovi/rebuild_hashtable'", "yes\n", nil).
		on("test -f /home/root/xovi/exthome/qt-resource-rebuilder/hashtab", "yes\n", nil).
		on("hexdump", hashtabHex("3.27.3.0"), nil).
		on("test -e", "", errorString("exit status 1")).
		on("update-engine", "disabled\n@@\n", nil)
}

func checkByID(checks []Check, id string) (Check, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

func TestChecksOnAHealthyTablet(t *testing.T) {
	s := newTestService(standardCatalog(), healthyTablet())

	checks := s.Checks(false)
	for _, c := range checks {
		if !c.OK {
			t.Errorf("unexpected problem: %+v", c)
		}
	}
	if c, ok := checkByID(checks, "hashtable"); !ok || c.Title != "The hashtable matches OS 3.27.3.0" {
		t.Errorf("hashtable check = %+v", c)
	}
	if _, ok := checkByID(checks, "auto-updates"); !ok {
		t.Error("the auto-update state should be reported")
	}
}

func TestChecksFindProblems(t *testing.T) {
	cases := []struct {
		name   string
		tablet func() *fakeTablet
		id     string
		title  string
		fix    string
	}{
		{"hashtable missing", func() *fakeTablet {
			return prepend(healthyTablet(), "test -f /home/root/xovi/exthome/qt-resource-rebuilder/hashtab", "no\n")
		}, "hashtable", "The hashtable is missing", FixRebuildHashtable},
		{"hashtable for an older OS", func() *fakeTablet {
			return prepend(healthyTablet(), "hexdump", hashtabHex("3.26.0.68"))
		}, "hashtable", "The hashtable is for another OS version", FixRebuildHashtable},
		{"tripletap not installed", func() *fakeTablet {
			return prepend(healthyTablet(), "list -I", coreInstalled)
		}, "tripletap", "Triple-tap is not installed", FixInstallTripletap},
		{"tripletap left paused", func() *fakeTablet {
			return prepend(healthyTablet(), "test -e", "yes\n")
		}, "tripletap", "Triple-tap is paused", FixResumeTripletap},
		{"automatic updates on", func() *fakeTablet {
			return prepend(healthyTablet(), "update-engine", "enabled\n@@\n")
		}, "auto-updates", "Automatic OS updates are on", ""},
		{"OS changed", func() *fakeTablet {
			return prepend(healthyTablet(), "state/osver", "3.26.0.68\n")
		}, "os-changed", "The OS changed since Vellum last ran", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestService(standardCatalog(), c.tablet())
			got, ok := checkByID(s.Checks(false), c.id)
			if !ok || got.OK || got.Title != c.title || got.Fix != c.fix {
				t.Errorf("check = %+v (found %v), want %q with fix %q", got, ok, c.title, c.fix)
			}
		})
	}
}

func TestAutoUpdatesOffInXochitlSettingsCountsAsOff(t *testing.T) {
	s := newTestService(standardCatalog(), prepend(healthyTablet(), "update-engine", "enabled\n@@\nAutoUpdate=false\n"))
	if c, _ := checkByID(s.Checks(false), "auto-updates"); !c.OK {
		t.Errorf("check = %+v", c)
	}
}

func TestPausedTripletapIsNotReportedDuringAJob(t *testing.T) {
	s := newTestService(standardCatalog(), prepend(healthyTablet(), "test -e", "yes\n"))
	if c, ok := checkByID(s.Checks(true), "tripletap"); ok {
		t.Errorf("a rebuild in progress pauses triple-tap on purpose, got %+v", c)
	}
}

func TestResumeTripletap(t *testing.T) {
	tablet := healthyTablet()
	s := newTestService(standardCatalog(), tablet)

	if err := s.ResumeTripletap(); err != nil {
		t.Fatal(err)
	}
	if !tablet.ran("systemctl start xovi-tripletap") || tablet.ran("xovi/start") {
		t.Errorf("want only triple-tap started, ran %v", tablet.commands)
	}
}

// prepend puts a rule ahead of the tablet's others, so it wins.
func prepend(f *fakeTablet, match, output string) *fakeTablet {
	f.rules = append([]rule{{match, output, nil}}, f.rules...)
	return f
}

func TestHashtabHexParses(t *testing.T) {
	if !strings.HasPrefix(hashtabHex("3.27.3.0"), "0000000000000000") {
		t.Fatal("helper is broken")
	}
}

func TestChecksReportMissingCorePackages(t *testing.T) {
	s := newTestService(standardCatalog(), prepend(healthyTablet(), "info -q", "tripletap\n"))
	if c, ok := checkByID(s.Checks(false), "vellum"); !ok || c.OK || !strings.Contains(c.Detail, "vellum, remarkable-os") {
		t.Errorf("check = %+v", c)
	}
}
