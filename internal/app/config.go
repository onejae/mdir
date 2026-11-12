package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Launcher struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type Config struct {
	Launchers map[string]Launcher `json:"launchers"`
}

type State struct {
	History  []string          `json:"history"`
	Comments map[string]string `json:"comments"`
}

const maxJSONBytes = 4 << 20

func LoadConfig(path string) (Config, error) {
	c := Config{Launchers: make(map[string]Launcher)}
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return c, err
		}
		path = filepath.Join(dir, "mdir", "config.json")
	}
	if err := readJSON(path, &c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	for key, launcher := range c.Launchers {
		if len(key) != 2 || key[0] != 'F' || key[1] < '2' || key[1] > '9' {
			return c, fmt.Errorf("launcher %q: use F2 through F9", key)
		}
		if launcher.Label == "" || launcher.Command == "" {
			return c, fmt.Errorf("launcher %s needs label and command", key)
		}
	}
	return c, nil
}

func StatePath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "mdir", "state.json"), nil
}

func loadState(path string) (State, error) {
	s := State{Comments: make(map[string]string)}
	err := readJSON(path, &s)
	if s.Comments == nil {
		s.Comments = make(map[string]string)
	}
	return s, err
}

func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, maxJSONBytes)).Decode(value)
}

func saveState(path string, state State) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > maxJSONBytes {
		return fmt.Errorf("state exceeds 4 MiB; remove some comments before saving")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
