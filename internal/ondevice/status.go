package ondevice

import (
	"fmt"
	"strings"

	"reManager/internal/executor"
	"reManager/internal/vellum"
)

// Check is one line of the System tab's status: a fact about the tablet, and what can be done about it from here.
type Check struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Fix names what the frontend offers: "rebuild-hashtable", "resume-tripletap" or "install-tripletap". Empty when the fix needs a computer.
	Fix string `json:"fix,omitempty"`
}

const (
	FixRebuildHashtable = "rebuild-hashtable"
	FixResumeTripletap  = "resume-tripletap"
	FixInstallTripletap = "install-tripletap"
)

// Changing these needs the root partition, which this app does not write to.
const fromAComputer = "Fixing this changes system files, which this app does not do. Use desktop reManager."

// Checks looks the tablet over. Each check that cannot be read is left out rather than guessed. running says whether a job is under way, so a paused triple-tap is not mistaken for one left behind.
func (s *Service) Checks(running bool) []Check {
	checks := []Check{}
	installed, installedErr := s.vellum.ListInstalledWithVersions()

	if valid, missing, err := s.vellum.ValidateInstall(); err == nil && !valid {
		checks = append(checks, Check{ID: "vellum", Title: "Vellum is incomplete",
			Detail: "Missing: " + strings.Join(missing, ", ") + ". Reinstall Vellum from a computer."})
	}

	if state, err := s.vellum.GetOSVersionState(); err == nil && state.Mismatch {
		checks = append(checks, Check{ID: "os-changed", Title: "The OS changed since Vellum last ran",
			Detail: fmt.Sprintf("Vellum last ran on %s; this is %s. Mods may need `vellum reenable` and updates. %s", state.StoredVersion, state.CurrentVersion, fromAComputer)})
	} else if status, err := s.vellum.ReenableStatus(); err == nil && status == vellum.ReenableNeeded {
		checks = append(checks, Check{ID: "os-changed", Title: "Vellum needs re-enabling after an OS update",
			Detail: "Run `vellum reenable`. " + fromAComputer})
	}

	if check, ok := s.hashtableCheck(); ok {
		checks = append(checks, check)
	}

	if installedErr == nil {
		if _, ok := installed["tripletap"]; !ok {
			checks = append(checks, Check{ID: "tripletap", Title: "Triple-tap is not installed",
				Detail: "After the tablet restarts, xovi and this app stay off until something starts them. tripletap starts them with three presses of the power button.",
				Fix:    FixInstallTripletap})
		} else if !running && s.pathExists(tripletapPaused) {
			checks = append(checks, Check{ID: "tripletap", Title: "Triple-tap is paused",
				Detail: "A hashtable rebuild paused it and did not turn it back on.", Fix: FixResumeTripletap})
		}
	}

	if installedErr == nil {
		if check, ok := s.repositoryCheck(installed); ok {
			checks = append(checks, check)
		}
	}

	if enabled, ok := s.autoUpdatesOn(); ok {
		if enabled {
			checks = append(checks, Check{ID: "auto-updates", Title: "Automatic OS updates are on",
				Detail: "An OS update stops mods until each supports the new version and the hashtable is rebuilt. " + fromAComputer})
		} else {
			checks = append(checks, Check{ID: "auto-updates", OK: true, Title: "Automatic OS updates are off"})
		}
	}
	return checks
}

func (s *Service) hashtableCheck() (Check, bool) {
	if !s.fileExists(rebuildHashtable) {
		// No qt-resource-rebuilder, so no UI mods and no hashtable to have.
		return Check{}, false
	}
	checker := vellum.NewHashtabChecker(s.exec)
	exists, err := checker.CheckHashtabExists()
	if err != nil {
		return Check{}, false
	}
	if !exists {
		return Check{ID: "hashtable", Title: "The hashtable is missing",
			Detail: "UI mods do not work without it.", Fix: FixRebuildHashtable}, true
	}
	built, err := checker.GetHashtabVersion()
	firmware := s.firmware()
	switch {
	case err != nil || built == "" || firmware == "":
		return Check{ID: "hashtable", OK: true, Title: "The hashtable is there",
			Detail: "Which OS version it was built for is not recorded in it."}, true
	case built != firmware:
		return Check{ID: "hashtable", Title: "The hashtable is for another OS version",
			Detail: fmt.Sprintf("Built for %s; this is %s. UI mods may not work until it is rebuilt.", built, firmware), Fix: FixRebuildHashtable}, true
	default:
		return Check{ID: "hashtable", OK: true, Title: "The hashtable matches OS " + firmware}, true
	}
}

// autoUpdatesOn reads the same two switches desktop reManager does: the update service and xochitl's own setting.
func (s *Service) autoUpdatesOn() (enabled bool, ok bool) {
	out, err := s.exec.ExecuteWithOutput("systemctl is-enabled update-engine.service 2>/dev/null; echo @@; grep -m1 '^AutoUpdate=' /home/root/.config/remarkable/xochitl.conf 2>/dev/null")
	parts := strings.SplitN(out, "@@", 2)
	if len(parts) != 2 {
		return false, false
	}
	service := strings.TrimSpace(parts[0])
	if service == "" {
		return false, err == nil
	}
	return service == "enabled" && strings.TrimSpace(parts[1]) != "AutoUpdate=false", true
}

func (s *Service) pathExists(path string) bool {
	out, err := s.exec.ExecuteWithOutput("test -e " + executor.QuoteArg(path) + " && echo yes")
	return err == nil && strings.TrimSpace(out) == "yes"
}

// ResumeTripletap turns triple-tap back on after a rebuild that did not. The UI is already running, so nothing else happens.
func (s *Service) ResumeTripletap() error {
	_, err := s.exec.ExecuteWithOutput("if [ -f " + tripletapPaused + " ]; then rm -f " + tripletapPaused + "; systemctl start " + tripletapUnit + "; fi")
	return err
}
