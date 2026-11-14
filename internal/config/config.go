// Package config stores local board preferences. It never stores GitHub content.
package config

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkieltyka/todos/internal/domain"
	"go.yaml.in/yaml/v3"
)

type Paths struct{ ConfigFile, DraftDir string }
type File struct {
	Version int                    `yaml:"version"`
	Boards  map[string]BoardChoice `yaml:"boards"`
	Extra   map[string]any         `yaml:",inline"`
}
type BoardChoice struct {
	Owner  string `yaml:"owner"`
	Number int    `yaml:"number"`
}
type Store struct {
	paths   Paths
	mu      sync.Mutex
	warning *domain.Error
	file    File
	write   func(string, []byte) error
}

func DefaultPaths(getenv func(string) string, home func() (string, error)) (Paths, error) {
	config, state := getenv("XDG_CONFIG_HOME"), getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(config) || !filepath.IsAbs(state) {
		h, err := home()
		if err != nil {
			return Paths{}, configError("could not find home directory", err)
		}
		if !filepath.IsAbs(h) {
			return Paths{}, configError("home directory must be absolute", nil)
		}
		if !filepath.IsAbs(config) {
			config = filepath.Join(h, ".config")
		}
		if !filepath.IsAbs(state) {
			state = filepath.Join(h, ".local", "state")
		}
	}
	return Paths{ConfigFile: filepath.Join(config, "todos", "config.yml"), DraftDir: filepath.Join(state, "todos", "drafts")}, nil
}
func New(paths Paths) *Store {
	s := &Store{paths: paths, write: atomicWrite}
	f, err := s.read()
	s.file = f
	s.setWarning(err)
	return s
}
func configError(message string, err error) *domain.Error {
	return &domain.Error{Kind: domain.ErrConfig, Message: message, Hint: "repair or remove the todos config file", Err: err}
}
func emptyFile() File {
	return File{Version: 1, Boards: map[string]BoardChoice{}, Extra: map[string]any{}}
}
func (s *Store) read() (File, error) {
	data, err := os.ReadFile(s.paths.ConfigFile)
	if os.IsNotExist(err) {
		return emptyFile(), nil
	}
	if err != nil {
		return emptyFile(), configError("could not read todos config", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var f File
	if err = decoder.Decode(&f); err != nil {
		return emptyFile(), configError("could not parse todos config", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return emptyFile(), configError("config must contain one YAML document", err)
	}
	if f.Version != 1 {
		return emptyFile(), configError(fmt.Sprintf("unsupported config version %d", f.Version), nil)
	}
	if f.Boards == nil {
		f.Boards = map[string]BoardChoice{}
	}
	if f.Extra == nil {
		f.Extra = map[string]any{}
	}
	for _, b := range f.Boards {
		if strings.TrimSpace(b.Owner) == "" || b.Number < 1 {
			return emptyFile(), configError("invalid remembered board", nil)
		}
	}
	return f, nil
}
func key(r domain.RepoRef) string { return strings.ToLower(r.Host + "/" + r.Owner + "/" + r.Name) }
func (s *Store) setWarning(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warning = nil
	if err != nil {
		s.warning = configError(err.Error(), err)
	}
}
func (s *Store) Warning() *domain.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.CloneError(s.warning)
}
func (s *Store) RememberedBoard(ctx context.Context, r domain.RepoRef) (*domain.ProjectRef, error) {
	if ctx.Err() != nil {
		return nil, configError("config read interrupted", ctx.Err())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warning != nil {
		return nil, nil
	}
	b, ok := s.file.Boards[key(r)]
	if !ok {
		return nil, nil
	}
	return &domain.ProjectRef{Host: r.Host, Owner: b.Owner, Number: b.Number}, nil
}
func (s *Store) RememberBoard(ctx context.Context, r domain.RepoRef, p domain.ProjectRef) error {
	if ctx.Err() != nil {
		return configError("config save interrupted", ctx.Err())
	}
	if p.Number < 1 || strings.TrimSpace(p.Owner) == "" || !strings.EqualFold(p.Host, r.Host) {
		return configError("invalid remembered project reference", nil)
	}
	f, err := s.read()
	if err != nil {
		s.setWarning(err)
		return err
	}
	f.Boards[key(r)] = BoardChoice{Owner: p.Owner, Number: p.Number}
	data, err := yaml.Marshal(f)
	if err != nil {
		return configError("could not encode todos config", err)
	}
	if ctx.Err() != nil {
		return configError("config save interrupted", ctx.Err())
	}
	if err = s.write(s.paths.ConfigFile, data); err != nil {
		err = configError("could not save todos config", err)
		s.setWarning(err)
		return err
	}
	s.mu.Lock()
	s.file = f
	s.warning = nil
	s.mu.Unlock()
	return nil
}
func (s *Store) Check(ctx context.Context) (domain.DoctorCheck, error) {
	check := domain.DoctorCheck{Name: "Config", Detail: "config is readable and valid (or absent)"}
	if ctx.Err() != nil {
		return check, configError("config check interrupted", ctx.Err())
	}
	f, err := s.read()
	s.mu.Lock()
	s.file = f
	s.mu.Unlock()
	s.setWarning(err)
	if err != nil {
		check.Detail = err.Error()
		check.Hint = "repair or remove the todos config file"
		return check, err
	}
	check.OK = true
	return check, nil
}
