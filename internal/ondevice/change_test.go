package ondevice

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func tabletWithRetaskable() *fakeTablet {
	return (&fakeTablet{}).
		on("RELEASE_VERSION", "RELEASE_VERSION=3.27.3.0\n", nil).
		on("list -I", strings.Join([]string{
			"appload-0.5.3-r2 aarch64 {appload} (GPL-3.0) [installed]",
			"hide-dev-mode-icon-1.0.2-r8 aarch64 {hide-dev-mode-icon} (MIT) [installed]",
			"retaskable-1.0.5-r1 aarch64 {retaskable} (GPL-3.0-only) [installed]",
			"xovi-0.3.2-r0 aarch64 {xovi} (GPL-3.0) [installed]",
			"xovi-extensions-19.0.1-r0 aarch64 {xovi-extensions} (GPL-3.0) [installed]",
		}, "\n")+"\n", nil)
}

func TestPlanRemove(t *testing.T) {
	tablet := tabletWithRetaskable().on("del -s 'retaskable'", "(1/1) Purging retaskable (1.0.5-r1)\nOK: 40 MiB in 57 packages\n", nil)
	s := newTestService(standardCatalog(), tablet)

	plan, err := s.PlanRemove([]string{"retaskable"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Error != "" || plan.Action != ActionRemove || !reflect.DeepEqual(plan.Packages, []string{"retaskable"}) {
		t.Errorf("plan = %+v", plan)
	}
	if tablet.ran("vellum del 'retaskable'") {
		t.Error("planning must not remove anything")
	}
}

func TestPlanRemoveRefuses(t *testing.T) {
	cases := []struct {
		name    string
		request string
		output  string
		want    string
	}{
		{
			name:    "what the app runs on",
			request: "appload",
			want:    "appload is part of what this app runs on",
		},
		{
			name:    "a package that is not installed",
			request: "koreader",
			want:    "koreader is not installed",
		},
		{
			name:    "a package other mods need",
			request: "xovi-extensions",
			output:  "World updated, but the following packages are not removed due to:\n  xovi-extensions: retaskable hide-dev-mode-icon\n\nOK: 41 MiB in 58 packages\n",
			want:    "xovi-extensions is needed by hide-dev-mode-icon, retaskable. Remove those first.",
		},
		{
			name:    "a removal that would take the app's base with it",
			request: "xovi-extensions",
			output:  "(1/2) Purging xovi-extensions (19.0.1-r0)\n(2/2) Purging appload (0.5.3-r2)\n",
			want:    "This would also remove appload",
		},
		{
			name:    "a removal Vellum would not carry out",
			request: "hide-dev-mode-icon",
			output:  "OK: 41 MiB in 58 packages\n",
			want:    "Vellum would leave hide-dev-mode-icon installed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tablet := tabletWithRetaskable().on("del -s", c.output, nil)
			s := newTestService(standardCatalog(), tablet)

			plan, err := s.PlanRemove([]string{c.request})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.Error, c.want) || len(plan.Packages) != 0 {
				t.Errorf("plan = %+v, want an error containing %q", plan, c.want)
			}

			result := s.Remove([]string{c.request}, nil)
			if result.Success || tablet.ran("vellum del '"+c.request+"'") {
				t.Errorf("a refused plan must not be carried out: result %+v, ran %v", result, tablet.commands)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	purge := "(1/1) Purging retaskable (1.0.5-r1)\nOK: 40 MiB in 57 packages\n"
	tablet := tabletWithRetaskable().
		on("del -s 'retaskable'", purge, nil).
		on("vellum del 'retaskable'", purge, nil)
	s := newTestService(standardCatalog(), tablet)

	var messages []string
	result := s.Remove([]string{"retaskable"}, func(p Progress) { messages = append(messages, p.Message) })

	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if !tablet.ran("vellum del 'retaskable'") {
		t.Errorf("Vellum was not asked to remove, ran %v", tablet.commands)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "Purging retaskable") {
		t.Errorf("progress should carry Vellum's output, got %q", messages)
	}
}

func TestRemoveReportsVellumFailure(t *testing.T) {
	tablet := tabletWithRetaskable().
		on("del -s 'retaskable'", "(1/1) Purging retaskable (1.0.5-r1)\n", nil).
		on("vellum del 'retaskable'", "ERROR: retaskable: IO ERROR\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if result := s.Remove([]string{"retaskable"}, nil); result.Success || len(result.Errors) == 0 {
		t.Errorf("result = %+v", result)
	}
}

const upgradeOutput = "(1/2) Upgrading xovi (0.3.2-r0 -> 0.3.3-r0)\n(2/2) Upgrading appload (0.5.3-r2 -> 0.5.3-r3)\nOK: 41 MiB in 58 packages\n"

func TestUpgrades(t *testing.T) {
	tablet := tabletWithRetaskable().on("upgrade --simulate", upgradeOutput, nil)
	s := newTestService(standardCatalog(), tablet)

	got, err := s.Upgrades()
	if err != nil {
		t.Fatal(err)
	}
	want := []Upgrade{
		{Name: "appload", From: "0.5.3-r2", To: "0.5.3-r3"},
		{Name: "xovi", From: "0.3.2-r0", To: "0.3.3-r0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upgrades = %+v, want %+v", got, want)
	}
	if !tablet.ran("vellum update") {
		t.Error("the index should be refreshed before looking for upgrades")
	}
	if tablet.ran("upgrade -y") {
		t.Error("looking for upgrades must not upgrade anything")
	}
}

func TestUpgradesReportsAFailedSolve(t *testing.T) {
	tablet := tabletWithRetaskable().on("upgrade --simulate", "", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if _, err := s.Upgrades(); err == nil {
		t.Error("a failed solve must not read as nothing to upgrade")
	}
}

func TestPlanUpgrade(t *testing.T) {
	tablet := tabletWithRetaskable().on("upgrade --simulate", upgradeOutput, nil)
	s := newTestService(standardCatalog(), tablet)

	plan, err := s.PlanUpgrade(nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != ActionUpgrade || plan.Requested == nil || len(plan.Requested) != 0 {
		t.Errorf("plan = %+v, want an upgrade of everything with an empty, non-null request", plan)
	}
	if !reflect.DeepEqual(plan.Packages, []string{"appload", "xovi"}) || plan.Upgrades["xovi"] != "0.3.2-r0 -> 0.3.3-r0" {
		t.Errorf("plan = %+v", plan)
	}

	if _, err := s.PlanUpgrade([]string{"xovi"}); err != nil {
		t.Fatal(err)
	}
	if !tablet.ran("upgrade --simulate --force-missing-repositories 'xovi'") {
		t.Errorf("a named upgrade should be solved for that name, ran %v", tablet.commands)
	}
	if _, err := s.PlanUpgrade([]string{"x; reboot"}); err == nil {
		t.Error("want an error for a bad name")
	}
}

func TestUpgrade(t *testing.T) {
	tablet := tabletWithRetaskable().
		on("upgrade --simulate", upgradeOutput, nil).
		on("upgrade -y", upgradeOutput, nil)
	s := newTestService(standardCatalog(), tablet)

	var last Progress
	result := s.Upgrade(nil, func(p Progress) {
		if p.Package != "" {
			last = p
		}
	})

	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if !tablet.ran("vellum upgrade -y") {
		t.Errorf("ran %v", tablet.commands)
	}
	if last.Package != "appload" || last.Index != 1 || last.Total != 2 {
		t.Errorf("last progress = %+v, want appload as 2 of 2", last)
	}
}

func TestUpgradeWithNothingToDoRunsNothing(t *testing.T) {
	tablet := tabletWithRetaskable().on("upgrade --simulate", "OK: 41 MiB in 58 packages\n", nil)
	s := newTestService(standardCatalog(), tablet)

	if result := s.Upgrade(nil, nil); !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if tablet.ran("upgrade -y") {
		t.Error("nothing to upgrade, so Vellum should not be run")
	}
}

func TestUpgradeReportsVellumFailure(t *testing.T) {
	tablet := tabletWithRetaskable().
		on("upgrade --simulate", upgradeOutput, nil).
		on("upgrade -y", "(1/2) Upgrading xovi (0.3.2-r0 -> 0.3.3-r0)\nERROR: xovi-0.3.3-r0: network error\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	result := s.Upgrade(nil, nil)
	if result.Success || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "network error") {
		t.Errorf("result = %+v", result)
	}
}

func TestInstallLoadsTheCatalogueInAFreshProcess(t *testing.T) {
	// The worker starts with nothing loaded; it has to pick the same provider the frontend's plan showed.
	catalog := standardCatalog()
	catalog.ready = false
	tablet := standardTablet().on("add --simulate", "(1/1) Installing koreader (1-r0)\n", nil)
	s := newTestService(catalog, tablet)

	if _, err := s.Plan([]string{"koreader"}); err != nil {
		t.Fatal(err)
	}
	if !catalog.ready {
		t.Error("planning should load the catalogue when it is not loaded yet")
	}
}

// What Vellum printed on the Move when retaskable was removed while koreader still needed "a launcher".
const unchosenLauncher = `ERROR: unable to select packages:
  launcher (virtual):
    note: please select one of the 'provided by'
          packages explicitly
    provided by: appload-0.5.3-r3
                 appload-0.4.2-r1
                 appload-0.6.0-r1 oxide-3.1.1-r3
                 oxide-3.1.2-r2
    required by: koreader-2026.07.1-r1[launcher]
`

func tabletWithLauncherQuestion() *fakeTablet {
	return (&fakeTablet{}).
		on("RELEASE_VERSION", "RELEASE_VERSION=3.27.3.0\n", nil).
		on("list -I", strings.Join([]string{
			"appload-0.5.3-r3 aarch64 {appload} (GPL-3.0) [installed]",
			"koreader-2026.07.1-r1 aarch64 {koreader} (AGPL-3.0) [installed]",
			"retaskable-1.0.5-r1 aarch64 {retaskable} (GPL-3.0-only) [installed]",
		}, "\n")+"\n", nil).
		on("del -s 'retaskable'", unchosenLauncher, errors.New("exit status 1")).
		on("add --simulate 'appload' '!retaskable'", "(1/1) Purging retaskable (1.0.5-r1)\nOK: 40 MiB in 57 packages\n", nil)
}

func TestPlanRemoveSettlesALauncherLeftUnchosen(t *testing.T) {
	tablet := tabletWithLauncherQuestion()
	s := newTestService(standardCatalog(), tablet)

	plan, err := s.PlanRemove([]string{"retaskable"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Error != "" || !reflect.DeepEqual(plan.Packages, []string{"retaskable"}) || !reflect.DeepEqual(plan.Keep, []string{"appload"}) {
		t.Errorf("plan = %+v, want retaskable removed and appload kept", plan)
	}
	if tablet.ran("vellum add 'appload'") || tablet.ran("vellum del 'retaskable'") {
		t.Errorf("planning must not change anything, ran %v", tablet.commands)
	}
}

func TestRemoveMarksTheKeptProviderFirst(t *testing.T) {
	tablet := tabletWithLauncherQuestion().
		on("vellum add 'appload'", "OK: 41 MiB in 58 packages\n", nil).
		on("vellum del 'retaskable'", "(1/1) Purging retaskable (1.0.5-r1)\n", nil)
	s := newTestService(standardCatalog(), tablet)

	result := s.Remove([]string{"retaskable"}, nil)
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	add, del := -1, -1
	for i, cmd := range tablet.commands {
		if strings.HasSuffix(cmd, "vellum add 'appload'") {
			add = i
		}
		if strings.HasSuffix(cmd, "vellum del 'retaskable'") {
			del = i
		}
	}
	if add < 0 || del < 0 || add > del {
		t.Errorf("want appload marked before retaskable is removed, ran %v", tablet.commands)
	}
}

func TestRemoveStopsIfTheProviderCannotBeMarked(t *testing.T) {
	tablet := tabletWithLauncherQuestion().on("vellum add 'appload'", "ERROR: something\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if result := s.Remove([]string{"retaskable"}, nil); result.Success || tablet.ran("vellum del 'retaskable'") {
		t.Errorf("result %+v, ran %v", result, tablet.commands)
	}
}

func TestInstalledProviders(t *testing.T) {
	installed := map[string]string{"appload": "0.5.3-r3", "koreader": "2026.07.1-r1"}
	if got, ok := installedProviders(unchosenLauncher, installed); !ok || !reflect.DeepEqual(got, []string{"appload"}) {
		t.Errorf("got %v %v", got, ok)
	}
	// Neither provider installed, or both: the choice is the user's, not the app's.
	if _, ok := installedProviders(unchosenLauncher, map[string]string{"koreader": "2026.07.1-r1"}); ok {
		t.Error("no installed provider should not be settled")
	}
	both := map[string]string{"appload": "0.5.3-r3", "oxide": "3.1.2-r2"}
	if _, ok := installedProviders(unchosenLauncher, both); ok {
		t.Error("two installed providers should not be settled")
	}
	// A different installed version is not a provider Vellum offered.
	if _, ok := installedProviders(unchosenLauncher, map[string]string{"appload": "0.5.2-r0"}); ok {
		t.Error("an installed version not in the list should not count")
	}
	if _, ok := installedProviders("ERROR: unable to select packages:\n  foo (no such package):\n", installed); ok {
		t.Error("a failure that is not about a virtual should not be settled")
	}
}
