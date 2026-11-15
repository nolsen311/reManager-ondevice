package ondevice

import (
	"errors"
	"strings"
	"testing"
)

func systemTablet() *fakeTablet {
	return standardTablet().on("test -x", "yes\n", nil)
}

// indexOf finds the first command that runs substr, skipping the existence checks that only name it.
func indexOf(commands []string, substr string) int {
	for i, cmd := range commands {
		if strings.HasPrefix(cmd, "test -x") {
			continue
		}
		if strings.Contains(cmd, substr) {
			return i
		}
	}
	return -1
}

func TestRestart(t *testing.T) {
	tablet := systemTablet()
	s := newTestService(standardCatalog(), tablet)

	if result := s.Restart(nil, nil); !result.Success {
		t.Fatalf("result = %+v", result)
	}
	reset, start := indexOf(tablet.commands, "systemctl reset-failed xochitl"), indexOf(tablet.commands, "/home/root/xovi/start")
	if reset < 0 || start < 0 || reset != start {
		t.Errorf("want the start-limit reset in the same command as xovi's start, ran %v", tablet.commands)
	}
}

func TestRestartWithoutXovi(t *testing.T) {
	tablet := standardTablet().on("test -x", "", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if result := s.Restart(nil, nil); result.Success || indexOf(tablet.commands, "xovi/start") >= 0 {
		t.Errorf("result %+v, ran %v", result, tablet.commands)
	}
}

func TestRebuildHashtableStartsTheUIWithModsAfterwards(t *testing.T) {
	tablet := systemTablet()
	s := newTestService(standardCatalog(), tablet)

	var messages []string
	result := s.RebuildHashtable(nil, func(p Progress) { messages = append(messages, p.Message) })
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	rebuild := indexOf(tablet.commands, "/home/root/xovi/rebuild_hashtable")
	start := indexOf(tablet.commands, "/home/root/xovi/start")
	if rebuild < 0 || start < 0 || start < rebuild {
		t.Errorf("want the rebuild, then xovi's start, ran %v", tablet.commands)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "passcode") {
		t.Errorf("the screen should mention the passcode prompt, got %q", messages)
	}
}

func TestRebuildHashtableStillStartsTheUIWhenItFails(t *testing.T) {
	tablet := standardTablet().
		on("test -x", "yes\n", nil).
		on("/home/root/xovi/rebuild_hashtable", "something broke\n", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	result := s.RebuildHashtable(nil, nil)
	if result.Success || len(result.Errors) != 1 {
		t.Errorf("result = %+v", result)
	}
	if indexOf(tablet.commands, "/home/root/xovi/start") < 0 {
		t.Errorf("the UI must be started again after a failed rebuild, ran %v", tablet.commands)
	}
}

func TestRebuildHashtableGoesThroughAnActiveLauncher(t *testing.T) {
	tablet := standardTablet().
		on("test -x", "yes\n", nil).
		on("current-launcher", "oxide\n@@launcherctl@@\noxide\n", nil)
	s := newTestService(standardCatalog(), tablet)

	if result := s.RebuildHashtable(nil, nil); !result.Success {
		t.Fatalf("result = %+v", result)
	}
	rebuild := indexOf(tablet.commands, "/home/root/xovi/rebuild_hashtable")
	if rebuild < 0 || !strings.Contains(tablet.commands[rebuild], "stop-launcher") || !strings.Contains(tablet.commands[rebuild], "start-launcher") {
		t.Errorf("want the launcher stopped and started around the rebuild, ran %v", tablet.commands)
	}
	if indexOf(tablet.commands, "/home/root/xovi/start") >= 0 {
		t.Errorf("the launcher brings the UI back, so xovi's start must not run as well, ran %v", tablet.commands)
	}
}

func TestRebuildHashtableWithoutTheRebuilder(t *testing.T) {
	tablet := standardTablet().on("test -x", "", errors.New("exit status 1"))
	s := newTestService(standardCatalog(), tablet)

	if result := s.RebuildHashtable(nil, nil); result.Success || indexOf(tablet.commands, "rebuild_hashtable") >= 0 {
		t.Errorf("result %+v, ran %v", result, tablet.commands)
	}
}

func TestSystemJobsTakeNoPackages(t *testing.T) {
	for _, action := range []string{ActionRestart, ActionRebuildHashtable} {
		if err := checkJob(action, nil); err != nil {
			t.Errorf("%s: %v", action, err)
		}
		if err := checkJob(action, []string{"xovi"}); err == nil {
			t.Errorf("%s with a package should be refused", action)
		}
	}
	if err := checkJob("reboot", nil); err == nil {
		t.Error("an unknown action should be refused")
	}
}
