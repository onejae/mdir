package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigAndStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(`{"launchers":{"F2":{"label":"Editor","command":"vim \"$2\""}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(config)
	if err != nil || c.Launchers["F2"].Label != "Editor" {
		t.Fatalf("config: %#v %v", c, err)
	}
	stateFile := filepath.Join(dir, "state", "state.json")
	want := State{History: []string{"/tmp/한글"}, Comments: map[string]string{"/tmp/one.txt": "a note"}}
	if err := saveState(stateFile, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(stateFile)
	if err != nil || got.Comments["/tmp/one.txt"] != "a note" || got.History[0] != "/tmp/한글" {
		t.Fatalf("state: %#v %v", got, err)
	}
	info, _ := os.Stat(stateFile)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("state mode: %o", info.Mode().Perm())
	}
}

func TestInvalidLauncherAndBrokenStateAreRejected(t *testing.T) {
	a, s := testApp(t)
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"launchers":{"F10":{"label":"reserved","command":"true"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(config); err == nil {
		t.Fatal("reserved F10 launcher accepted")
	}
	if err := os.WriteFile(a.StateFile, []byte("broken json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(s, a.Cwd, Config{}, a.StateFile); err == nil {
		t.Fatal("broken state silently loaded")
	}
	data, _ := os.ReadFile(a.StateFile)
	if string(data) != "broken json" {
		t.Fatal("broken state was overwritten")
	}
}

func TestOversizedStatePreservesReadablePreviousFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	previous := State{Comments: map[string]string{"/old": "kept"}}
	if err := saveState(file, previous); err != nil {
		t.Fatal(err)
	}
	oversized := State{Comments: map[string]string{"/large": strings.Repeat("x", maxJSONBytes)}}
	if err := saveState(file, oversized); err == nil {
		t.Fatal("oversized state accepted")
	}
	state, err := loadState(file)
	if err != nil || state.Comments["/old"] != "kept" {
		t.Fatal("previous state damaged")
	}
}
