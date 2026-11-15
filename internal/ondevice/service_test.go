package ondevice

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	rmdevice "github.com/rmitchellscott/remarkable-go/device"

	"reManager/internal/component"
	"reManager/internal/vellum"
)

var move = Device{Type: rmdevice.RMPPMove, Arch: rmdevice.Aarch64}

// fakeTablet answers the shell commands the Vellum client sends. Each rule is matched against the command as a substring, first match wins.
type fakeTablet struct {
	// err, when set, is what the tablet reports as the reason its job was stopped.
	err      error
	mu       sync.Mutex
	rules    []rule
	commands []string
}

type rule struct {
	match  string
	output string
	err    error
}

func (f *fakeTablet) on(match, output string, err error) *fakeTablet {
	f.rules = append(f.rules, rule{match, output, err})
	return f
}

func (f *fakeTablet) answer(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	for _, r := range f.rules {
		if strings.Contains(cmd, r.match) {
			return r.output, r.err
		}
	}
	return "", nil
}

func (f *fakeTablet) ran(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cmd := range f.commands {
		if strings.Contains(cmd, substr) {
			return true
		}
	}
	return false
}

func (f *fakeTablet) ExecuteWithOutput(cmd string) (string, error) { return f.answer(cmd) }
func (f *fakeTablet) Err() error                                   { return f.err }
func (f *fakeTablet) Execute([]component.CommandResult) error      { return nil }
func (f *fakeTablet) ExecuteStreaming(cmd string, onOutput func(string)) error {
	out, err := f.answer(cmd)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if onOutput != nil && line != "" {
			onOutput(line)
		}
	}
	return err
}

type fakeCatalog struct {
	packages   []vellum.Package
	choices    []vellum.VirtualChoice
	ready      bool
	refreshErr error
	gotDevice  string
	gotOS      string
	gotArch    string
}

func (c *fakeCatalog) Refresh() error {
	if c.refreshErr != nil {
		return c.refreshErr
	}
	c.ready = true
	return nil
}
func (c *fakeCatalog) Ready() bool { return c.ready }
func (c *fakeCatalog) GetAllPackagesForDevice(deviceType, firmware, arch string) []vellum.Package {
	c.gotDevice, c.gotOS, c.gotArch = deviceType, firmware, arch
	return c.packages
}
func (c *fakeCatalog) ResolveVirtualChoices(requested, installed []string, deviceType, firmware, arch string) []vellum.VirtualChoice {
	return c.choices
}

func standardTablet() *fakeTablet {
	return (&fakeTablet{}).
		on("RELEASE_VERSION", "RELEASE_VERSION=3.27.3.0\n", nil).
		on("list -I", "appload-0.5.3-r2 aarch64 {appload} (GPL-3.0) [installed]\nxovi-0.3.3-r0 aarch64 {xovi} (GPL-3.0) [installed]\n", nil)
}

func standardCatalog() *fakeCatalog {
	return &fakeCatalog{ready: true, packages: []vellum.Package{
		{Name: "koreader", Version: "2026.07.1-r0", Description: "An ebook reader", Categories: []string{"apps"}, Compatible: true, Depends: []string{"launcher", "vellum"}},
		{Name: "appload", Version: "0.6.0-r1", Description: "Launcher extension", Categories: []string{"launchers"}, Compatible: false, IncompatibleReason: "needs OS 3.28"},
		{Name: "vellum", Version: "1.0.0-r0", Description: "The package manager", Compatible: true},
		{Name: "rmppmove", Version: "1", Compatible: true},
		{Name: "xovi", Version: "0.3.3-r0", Description: "Core framework", Compatible: true},
	}}
}

func newTestService(catalog *fakeCatalog, tablet *fakeTablet) *Service {
	return NewService(catalog, vellum.NewMetadataStore(), tablet, move)
}

func names(packages []PackageSummary) []string {
	out := []string{}
	for _, p := range packages {
		out = append(out, p.Name)
	}
	return out
}

func TestListSortsHidesAndMarksInstalled(t *testing.T) {
	catalog := standardCatalog()
	s := newTestService(catalog, standardTablet())

	got, err := s.List(ListRequest{IncludeIncompatible: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"appload", "koreader", "xovi"}; !reflect.DeepEqual(names(got.Packages), want) {
		t.Errorf("packages = %v, want %v (sorted, without vellum and the device's virtual package)", names(got.Packages), want)
	}
	if got.Total != 3 {
		t.Errorf("total = %d, want 3", got.Total)
	}
	appload := got.Packages[0]
	if !appload.Installed || appload.InstalledVersion != "0.5.3-r2" || appload.Compatible {
		t.Errorf("appload = %+v, want installed at 0.5.3-r2 and the catalogue version marked incompatible", appload)
	}
	if got.Packages[1].Installed {
		t.Error("koreader is not installed")
	}
	if catalog.gotDevice != "rmppmove" || catalog.gotOS != "3.27.3.0" || catalog.gotArch != "aarch64" {
		t.Errorf("catalogue asked for %s/%s/%s", catalog.gotDevice, catalog.gotOS, catalog.gotArch)
	}
}

func TestListFiltersAndPages(t *testing.T) {
	s := newTestService(standardCatalog(), standardTablet())

	got, _ := s.List(ListRequest{Query: " EBOOK "})
	if want := []string{"koreader"}; !reflect.DeepEqual(names(got.Packages), want) {
		t.Errorf("query matched %v, want %v", names(got.Packages), want)
	}
	got, _ = s.List(ListRequest{InstalledOnly: true})
	if want := []string{"appload", "xovi"}; !reflect.DeepEqual(names(got.Packages), want) {
		t.Errorf("installed = %v, want %v", names(got.Packages), want)
	}
	got, _ = s.List(ListRequest{Offset: 1, Limit: 1, IncludeIncompatible: true})
	if want := []string{"koreader"}; !reflect.DeepEqual(names(got.Packages), want) || got.Total != 3 || got.Offset != 1 {
		t.Errorf("page = %v total %d offset %d", names(got.Packages), got.Total, got.Offset)
	}
	got, _ = s.List(ListRequest{Offset: 99, IncludeIncompatible: true})
	if len(got.Packages) != 0 || got.Offset != 3 {
		t.Errorf("a page past the end should be empty, got %v at %d", names(got.Packages), got.Offset)
	}
}

func TestListCapsPageSize(t *testing.T) {
	catalog := &fakeCatalog{ready: true}
	for i := range MaxPageSize + 10 {
		catalog.packages = append(catalog.packages, vellum.Package{Name: "pkg" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Compatible: true})
	}
	s := newTestService(catalog, standardTablet())

	got, _ := s.List(ListRequest{Limit: 1000})
	if len(got.Packages) != MaxPageSize {
		t.Errorf("page of %d, want the cap of %d", len(got.Packages), MaxPageSize)
	}
}

func TestListLoadsTheCatalogueOnFirstUse(t *testing.T) {
	catalog := standardCatalog()
	catalog.ready = false
	s := newTestService(catalog, standardTablet())

	if _, err := s.List(ListRequest{}); err != nil {
		t.Fatal(err)
	}
	if !catalog.ready {
		t.Error("the catalogue should have been fetched")
	}

	offline := &fakeCatalog{refreshErr: errors.New("no route to host")}
	if _, err := newTestService(offline, standardTablet()).List(ListRequest{}); err == nil || !strings.Contains(err.Error(), "no route to host") {
		t.Errorf("want the fetch failure reported, got %v", err)
	}
}

func TestDetail(t *testing.T) {
	s := newTestService(standardCatalog(), standardTablet())

	got, err := s.Detail("koreader")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"launcher"}; !reflect.DeepEqual(got.Depends, want) {
		t.Errorf("depends = %v, want %v (vellum itself is not shown)", got.Depends, want)
	}
	if _, err := s.Detail("nope"); err == nil {
		t.Error("want an error for a package that is not in the catalogue")
	}
}

func TestPlan(t *testing.T) {
	tablet := standardTablet().on("add --simulate", strings.Join([]string{
		"(1/3) Installing luajit (2.1-r0)",
		"(2/3) Upgrading xovi (0.3.2-r0 -> 0.3.3-r0)",
		"(3/3) Installing koreader (2026.07.1-r0)",
		"OK: 41 MiB in 58 packages",
	}, "\n"), nil)
	s := newTestService(standardCatalog(), tablet)

	plan, err := s.Plan([]string{"koreader"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"luajit", "xovi", "koreader"}; !reflect.DeepEqual(plan.Packages, want) {
		t.Errorf("packages = %v, want %v", plan.Packages, want)
	}
	if want := map[string]string{"xovi": "0.3.2-r0 -> 0.3.3-r0"}; !reflect.DeepEqual(plan.Upgrades, want) {
		t.Errorf("upgrades = %v, want %v", plan.Upgrades, want)
	}
	if tablet.ran("vellum add 'koreader'") {
		t.Error("planning must not install anything")
	}
}

func TestPlanAddsThePreferredProvider(t *testing.T) {
	catalog := standardCatalog()
	catalog.choices = []vellum.VirtualChoice{{
		Virtual:   "launcher",
		Default:   "appload",
		Providers: []vellum.ProviderOption{{Name: "oxide", Compatible: true}, {Name: "appload", Compatible: true}},
	}}
	tablet := standardTablet().on("add --simulate", "(1/1) Installing koreader (1-r0)\n", nil)
	s := newTestService(catalog, tablet)

	if _, err := s.Plan([]string{"koreader"}); err != nil {
		t.Fatal(err)
	}
	if !tablet.ran("add --simulate 'appload' 'koreader'") {
		t.Errorf("the default provider should be simulated with the request, ran %v", tablet.commands)
	}
}

func TestPlanReportsWhatVellumCannotSatisfy(t *testing.T) {
	failure := "ERROR: unable to select packages:\n  appload-0.6.0-r1:\n    breaks: world[remarkable-os>=3.28]\n"
	tablet := standardTablet().on("add --simulate", failure, errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	plan, err := s.Plan([]string{"appload"})
	if err != nil {
		t.Fatalf("an unsatisfiable request is a plan with an explanation, not an error: %v", err)
	}
	if !strings.Contains(plan.Error, "unable to select packages") || len(plan.Packages) != 0 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanRejectsBadNames(t *testing.T) {
	tablet := standardTablet()
	s := newTestService(standardCatalog(), tablet)

	for _, bad := range [][]string{nil, {"ok", "; rm -rf /"}, {"--force"}} {
		if _, err := s.Plan(bad); err == nil {
			t.Errorf("Plan(%q) should fail", bad)
		}
	}
	if tablet.ran("add") {
		t.Errorf("nothing should reach Vellum, ran %v", tablet.commands)
	}
}

func TestInstall(t *testing.T) {
	steps := "(1/2) Installing luajit (2.1-r0)\n(2/2) Installing koreader (2026.07.1-r0)\nOK: 41 MiB in 58 packages\n"
	tablet := standardTablet().
		on("add --simulate", steps, nil).
		on("vellum add 'koreader'", steps, nil)
	s := newTestService(standardCatalog(), tablet)

	var progress []Progress
	result := s.Install([]string{"koreader"}, func(p Progress) { progress = append(progress, p) })

	if !result.Success || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !tablet.ran("vellum add 'koreader'") {
		t.Errorf("Vellum was not asked to install, ran %v", tablet.commands)
	}
	var sawKoreader bool
	for _, p := range progress {
		if p.Package == "koreader" && p.Index == 1 && p.Total == 2 {
			sawKoreader = true
		}
	}
	if !sawKoreader {
		t.Errorf("progress should name koreader as 2 of 2, got %+v", progress)
	}
}

func TestInstallDefersPostInstallSteps(t *testing.T) {
	// qt-resource-rebuilder asks for a hashtable rebuild, which stops the stock UI.
	steps := "(1/1) Installing qt-resource-rebuilder (18.0.0-r0)\n"
	tablet := standardTablet().
		on("add --simulate", steps, nil).
		on("vellum add 'qt-resource-rebuilder'", steps, nil)
	s := newTestService(standardCatalog(), tablet)

	result := s.Install([]string{"qt-resource-rebuilder"}, nil)

	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Deferred) != 1 {
		t.Fatalf("want the hashtable step reported as deferred, got %+v", result)
	}
	if tablet.ran("rebuild_hashtable") {
		t.Errorf("the deferred step must not run, ran %v", tablet.commands)
	}
}

func TestInstallStopsWhenThePlanFails(t *testing.T) {
	tablet := standardTablet().on("add --simulate", "ERROR: unable to select packages:\n  nope (no such package):\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	result := s.Install([]string{"nope"}, nil)

	if result.Success || len(result.Errors) == 0 {
		t.Errorf("result = %+v", result)
	}
	if tablet.ran("vellum add 'nope'") {
		t.Error("nothing should be installed when the plan fails")
	}
}

func TestInstallReportsVellumFailure(t *testing.T) {
	tablet := standardTablet().
		on("add --simulate", "(1/1) Installing koreader (1-r0)\n", nil).
		on("vellum add 'koreader'", "ERROR: koreader-1-r0: IO ERROR\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	result := s.Install([]string{"koreader"}, nil)

	if result.Success || len(result.Errors) == 0 {
		t.Errorf("result = %+v", result)
	}
}

func TestListHidesWhatCannotBeInstalledHere(t *testing.T) {
	catalog := standardCatalog()
	catalog.packages = append(catalog.packages, vellum.Package{Name: "bluetooth-settings", Description: "Bluetooth", Compatible: false})
	s := newTestService(catalog, standardTablet())

	got, err := s.List(ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// appload is incompatible in the catalogue but installed, so it stays.
	if want := []string{"appload", "koreader", "xovi"}; !reflect.DeepEqual(names(got.Packages), want) {
		t.Errorf("packages = %v, want %v", names(got.Packages), want)
	}
	if got.Total != 3 || got.Incompatible != 1 {
		t.Errorf("total %d, incompatible %d, want 3 and 1", got.Total, got.Incompatible)
	}

	got, _ = s.List(ListRequest{IncludeIncompatible: true})
	if got.Total != 4 || got.Incompatible != 0 {
		t.Errorf("with the toggle on: total %d, incompatible %d", got.Total, got.Incompatible)
	}
}
