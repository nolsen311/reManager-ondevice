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
		return InstallResult{Errors: []string{"Restart failed: " + err.Error()}}
	}
	return InstallResult{Success: true}
}

// RebuildHashtable runs the same rebuild desktop reManager offers, then starts the UI again the way that rebuild leaves it to be started: through the launcher if one is in charge, otherwise with mods.
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
	rebuildErr := s.streamScript(hook.Command.Script, "hashtable", onProgress)

	// Whatever happened, the UI must come back; the rebuild leaves it stopped.
	var start string
	if post := hook.DialogConfig.PostCommandDialog; post != nil && post.PrimaryAction != "" {
		for _, action := range post.Actions {
			if action.Id == post.PrimaryAction && action.Type == "run_command" {
				start = action.Value
			}
		}
	}
	var errs []string
	if rebuildErr != nil {
		errs = append(errs, "The hashtable rebuild failed: "+rebuildErr.Error())
	}
	if start != "" {
		if err := s.streamScript(start, "xovi", onProgress); err != nil {
			errs = append(errs, "The UI did not start again: "+err.Error()+". Restart the tablet to get the stock UI back.")
		}
	}
	return InstallResult{Success: len(errs) == 0, Errors: errs}
}
