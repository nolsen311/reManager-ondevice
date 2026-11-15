package ondevice

import (
	"fmt"
	"strings"

	"reManager/internal/component"
	"reManager/internal/executor"
	"reManager/internal/vellum"
)

// Jobs that restart the reMarkable UI. The UI this app is drawn in goes with it, so they only run in the worker.
const (
	ActionRestart          = "restart"
	ActionRebuildHashtable = "rebuild-hashtable"
)

const (
	xoviStart        = "/home/root/xovi/start"
	rebuildHashtable = "/home/root/xovi/rebuild_hashtable"
)

func isSystemAction(action string) bool {
	return action == ActionRestart || action == ActionRebuildHashtable
}

// SystemStatus says which system jobs this tablet can run.
type SystemStatus struct {
	CanRestart bool `json:"canRestart"`
	CanRebuild bool `json:"canRebuild"`
}

func (s *Service) fileExists(path string) bool {
	out, err := s.exec.ExecuteWithOutput("test -x " + executor.QuoteArg(path) + " && echo yes")
	return err == nil && strings.TrimSpace(out) == "yes"
}

func (s *Service) SystemStatus() SystemStatus {
	return SystemStatus{CanRestart: s.fileExists(xoviStart), CanRebuild: s.fileExists(rebuildHashtable)}
}

// streamScript runs one shell script, passing each line of output on as progress.
func (s *Service) streamScript(script, label string, onProgress func(Progress)) error {
	return s.exec.ExecuteStreaming(script, func(line string) {
		if onProgress != nil {
			onProgress(Progress{Package: label, Status: string(executor.StatusInstalling), Message: line})
		}
	})
}

// Restart restarts the reMarkable UI with mods, the same way xovi's own start script does. The start-limit reset comes first: on the Paper Pro line a fifth start in ten minutes reboots the tablet.
func (s *Service) Restart(_ []string, onProgress func(Progress)) InstallResult {
	if !s.fileExists(xoviStart) {
		return InstallResult{Errors: []string{"xovi's start script is missing, so the UI was not restarted."}}
	}
	if err := s.streamScript(executor.WithXochitlStartReset(xoviStart), "xovi", onProgress); err != nil {
		errs := []string{"Restart failed: " + err.Error()}
		if restoreErr := s.RestoreUI(onProgress); restoreErr != nil {
			errs = append(errs, "Afterwards, "+restoreErr.Error()+". Restart the tablet to get the stock UI back.")
		}
		return InstallResult{Errors: errs}
	}
	return InstallResult{Success: true}
}

// While xovi-tripletap runs, a triple-press of the power button starts xovi. During a rebuild that reloads systemd and kills the temporary UI the rebuild depends on, and the temporary UI looks finished, which invites exactly that press. The service is paused for the rebuild; this file, on a tmpfs so a reboot forgets it, records that it needs starting again.
const (
	tripletapUnit   = "xovi-tripletap"
	tripletapPaused = "/run/remanager-ondevice-tripletap-paused"
)

func (s *Service) pauseTripletap() {
	s.exec.ExecuteWithOutput("if systemctl is-active --quiet " + tripletapUnit + "; then touch " + tripletapPaused + " && systemctl stop " + tripletapUnit + "; fi")
}

// uiStartCommand is how the UI is started after a rebuild: through the launcher if one is in charge, otherwise with mods.
func (s *Service) uiStartCommand(exec executor.CommandExecutor) (start string, launcher bool) {
	hook, err := vellum.RebuildHashtableDialog(component.CommandContext{Arch: s.device.Arch, Device: s.device.Type, Exec: exec})
	if err == nil && hook != nil && hook.DialogConfig != nil {
		if post := hook.DialogConfig.PostCommandDialog; post != nil && post.PrimaryAction != "" {
			for _, action := range post.Actions {
				if action.Id == post.PrimaryAction && action.Type == "run_command" {
					return action.Value, post.PrimaryAction == "start_launcher"
				}
			}
		}
		if hook.DialogConfig.PostCommandDialog != nil && hook.DialogConfig.PostCommandDialog.InfoOnly {
			// The launcher was running and the rebuild restarts it itself.
			return executor.WithXochitlStartReset(vellum.LauncherctlBin + " start-launcher"), true
		}
	}
	return executor.WithXochitlStartReset(xoviStart), false
}

// RestoreUI starts the reMarkable UI if it is not running, and resumes triple-tap if a rebuild paused it. It is safe to run any number of times, and it runs at the end of every restart or rebuild however that ends.
func (s *Service) RestoreUI(onProgress func(Progress)) error {
	exec := s.rescueExec()
	start, launcher := s.uiStartCommand(exec)
	var check string
	if launcher {
		check = "[ -n \"$(" + vellum.LauncherctlBin + " active-launcher 2>/dev/null | grep -v '^none$')\" ]"
	} else {
		// Right after a restart the UI can still be "activating"; starting it again then would count against the start limit.
		check = "case $(systemctl is-active xochitl) in active|activating|reloading) true ;; *) false ;; esac"
	}
	var errs []string
	err := exec.ExecuteStreaming(check+" || { "+start+"; }", func(line string) {
		if onProgress != nil {
			onProgress(Progress{Package: "xovi", Status: string(executor.StatusInstalling), Message: line})
		}
	})
	if err != nil {
		errs = append(errs, "the UI did not start: "+err.Error())
	}
	if _, err := exec.ExecuteWithOutput("if [ -f " + tripletapPaused + " ]; then rm -f " + tripletapPaused + "; systemctl start " + tripletapUnit + "; fi"); err != nil {
		errs = append(errs, "triple-tap was not turned back on: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// RebuildHashtable runs the same rebuild desktop reManager offers, then starts the UI again. The rebuild stops the UI and runs a temporary one without mods until the hashtable is saved, so the UI is brought back however the rebuild ends.
func (s *Service) RebuildHashtable(_ []string, onProgress func(Progress)) InstallResult {
	if !s.fileExists(rebuildHashtable) {
		return InstallResult{Errors: []string{"qt-resource-rebuilder is not installed, so there is no hashtable to rebuild."}}
	}
	hook, err := vellum.RebuildHashtableDialog(component.CommandContext{Arch: s.device.Arch, Device: s.device.Type, Exec: s.exec})
	if err != nil || hook == nil || hook.Command == nil {
		return InstallResult{Errors: []string{fmt.Sprintf("Could not prepare the rebuild: %v", err)}}
	}
	if onProgress != nil {
		onProgress(Progress{Package: "hashtable", Status: string(executor.StatusInstalling), Message: "Rebuilding. If you have a passcode, enter it on the screen when asked."})
	}
	s.pauseTripletap()
	rebuildErr := s.streamScript(hook.Command.Script, "hashtable", onProgress)
	stopped := s.stopped()

	var errs []string
	switch {
	case stopped:
		errs = append(errs, "The rebuild was stopped before it finished, so the hashtable may be missing and UI mods may not work. Rebuild again and wait for the screen to restart a second time.")
	case rebuildErr != nil:
		errs = append(errs, "The hashtable rebuild failed: "+rebuildErr.Error())
	}
	if err := s.RestoreUI(onProgress); err != nil {
		errs = append(errs, "After the rebuild, "+err.Error()+". Restart the tablet to get the stock UI back.")
	}
	return InstallResult{Success: len(errs) == 0, Errors: errs}
}
