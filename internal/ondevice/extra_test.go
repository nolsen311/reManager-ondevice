package ondevice

import (
	"reflect"
	"testing"

	"reManager/internal/vellum"
)

// As the Move printed them, with this repo's package added.
const availableOutput = `WARNING: opening from cache https://example.org/aarch64/APKINDEX.tar.gz: No such file or directory
add-finished-button-1.0.0-r0 aarch64 {add-finished-button} (GPL-2.0)
add-finished-button-1.0.3-r1 aarch64 {add-finished-button} (GPL-2.0)
koreader-2025.10-r1 aarch64 {koreader} (AGPL-3.0)
koreader-2026.07.1-r1 aarch64 {koreader} (AGPL-3.0)
remanager-ondevice-0.1.0_pre1-r0 aarch64 {remanager-ondevice} (GPL-3.0-only)
remanager-ondevice-0.1.0-r0 aarch64 {remanager-ondevice} (GPL-3.0-only)
remanager-ondevice-0.0.9-r3 aarch64 {remanager-ondevice} (GPL-3.0-only)
rm2-only-mod-1.0.0-r0 aarch64 {rm2-only-mod} (MIT)
remanager-ondevice-doc-0.1.0-r0 aarch64 {remanager-ondevice} (GPL-3.0-only)
vellum-1.0.0-r0 aarch64 {vellum} (MIT)
`

const descriptionOutput = `remanager-ondevice-0.0.9-r3 description:
An old description.

remanager-ondevice-0.1.0-r0 description:
Browse, install, update and remove mods from the tablet itself,
without a computer

`

func TestNewestAvailable(t *testing.T) {
	got := newestAvailable(availableOutput)
	want := map[string]string{
		"add-finished-button":    "1.0.3-r1",
		"koreader":               "2026.07.1-r1",
		"remanager-ondevice":     "0.1.0-r0",
		"remanager-ondevice-doc": "0.1.0-r0",
		"rm2-only-mod":           "1.0.0-r0",
		"vellum":                 "1.0.0-r0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDescriptionsFor(t *testing.T) {
	got := descriptionsFor(descriptionOutput, map[string]string{"remanager-ondevice": "0.1.0-r0"})
	want := "Browse, install, update and remove mods from the tablet itself, without a computer"
	if got["remanager-ondevice"] != want {
		t.Errorf("got %q", got["remanager-ondevice"])
	}
}

func TestListIncludesPackagesFromOtherRepositories(t *testing.T) {
	catalog := standardCatalog()
	catalog.otherDevices = []vellum.Package{{Name: "rm2-only-mod"}, {Name: "add-finished-button"}}
	tablet := standardTablet().
		on("list --available", availableOutput, nil).
		on("info -d 'remanager-ondevice'", descriptionOutput, nil)
	s := newTestService(catalog, tablet)

	got, err := s.List(ListRequest{IncludeIncompatible: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"appload", "koreader", "remanager-ondevice", "xovi"}; !reflect.DeepEqual(names(got.Packages), want) {
		t.Errorf("packages = %v, want %v (not the catalogue's own, other devices', -doc or vellum)", names(got.Packages), want)
	}
	detail, err := s.Detail("remanager-ondevice")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Version != "0.1.0-r0" || detail.Category != ExtraSource || detail.Description == "" || !detail.Compatible {
		t.Errorf("detail = %+v", detail)
	}

	// Asked once, then served from the cache.
	before := len(tablet.commands)
	s.List(ListRequest{})
	for _, cmd := range tablet.commands[before:] {
		if cmd == vellum.VellumBin+" list --available" {
			t.Error("the available list should be cached")
		}
	}
}

func TestListWorksWhenVellumCannotListAvailablePackages(t *testing.T) {
	tablet := standardTablet().on("list --available", "", errorString("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if _, err := s.List(ListRequest{}); err != nil {
		t.Errorf("the catalogue's own packages should still list: %v", err)
	}
}
