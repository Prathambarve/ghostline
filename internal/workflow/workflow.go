// Package workflow stores user-authored saved commands ("workflows") — named,
// optionally-parameterized command templates surfaced in the command palette.
// Unlike history and the fix cache, these are explicitly authored by the user,
// not learned, so they carry no privacy gate.
package workflow

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Workflow struct {
	Name        string `yaml:"name" json:"name"`
	Command     string `yaml:"command" json:"command"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// file is the on-disk shape: a top-level "workflows:" list, so the YAML reads
// naturally and stays extensible.
type file struct {
	Workflows []Workflow `yaml:"workflows"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) load() []Workflow {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Workflows
}

func (s *Store) save(ws []Workflow) error {
	data, err := yaml.Marshal(file{Workflows: ws})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

// List returns all workflows sorted by name.
func (s *Store) List() []Workflow {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.load()
	sort.Slice(ws, func(i, j int) bool { return ws[i].Name < ws[j].Name })
	return ws
}

// Get returns the workflow with the given name.
func (s *Store) Get(name string) (Workflow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.load() {
		if w.Name == name {
			return w, true
		}
	}
	return Workflow{}, false
}

// Add inserts or replaces a workflow by name (upsert).
func (s *Store) Add(w Workflow) error {
	w.Name = strings.TrimSpace(w.Name)
	w.Command = strings.TrimSpace(w.Command)
	if w.Name == "" || w.Command == "" {
		return fmt.Errorf("workflow needs a name and a command")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.load()
	for i := range ws {
		if ws[i].Name == w.Name {
			ws[i] = w
			return s.save(ws)
		}
	}
	return s.save(append(ws, w))
}

// Remove deletes a workflow by name, reporting whether one was removed.
func (s *Store) Remove(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.load()
	out := ws[:0]
	removed := false
	for _, w := range ws {
		if w.Name == name {
			removed = true
			continue
		}
		out = append(out, w)
	}
	if !removed {
		return false, nil
	}
	return true, s.save(out)
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_-]+)\s*\}\}`)

// Expand substitutes {{name}} placeholders in a command using the given values.
// Unprovided placeholders are left intact so the user can fill them in the
// prompt buffer.
func Expand(command string, values map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(command, func(m string) string {
		key := placeholderRe.FindStringSubmatch(m)[1]
		if v, ok := values[key]; ok {
			return v
		}
		return m
	})
}
