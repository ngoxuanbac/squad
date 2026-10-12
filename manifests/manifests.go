// Package manifests keeps the team templates: the built-ins embedded here (this directory's
// *.yaml and prompts), the user's own in ~/.squad/templates, and reading them: who may write to
// whom, who may spawn whom, and what each role is told. A template is frozen into a team when the
// team is founded.
package manifests

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FS holds the built-in templates, the prompt files they name and the shared rules.
//
//go:embed *.yaml prompts rules
var FS embed.FS

// knownTools are the tools a role may be given.
var knownTools = []string{"send", "inbox", "who", "agent"}

type Manifest struct {
	Template     string           `yaml:"template" json:"template"`
	Summary      string           `yaml:"summary" json:"summary"`
	AutoJoinRole string           `yaml:"auto_join_role" json:"auto_join_role"`
	Taskforce    *Taskforce       `yaml:"taskforce" json:"taskforce,omitempty"`
	Roles        map[string]*Role `yaml:"roles" json:"roles"`
	Routing      []Route          `yaml:"routing" json:"routing"`
	Timers       []Timer          `yaml:"timers" json:"timers,omitempty"`
	Limits       Limits           `yaml:"limits" json:"limits"`
}

// Timer is a watch rule: a member of role On working for longer than SilentFor with no turn end
// is reported, once per working stretch, to Notify: its reports_to, or every member of a role.
type Timer struct {
	On        string `yaml:"on" json:"on"`
	SilentFor string `yaml:"silent_for" json:"silent_for"`
	Notify    string `yaml:"notify" json:"notify"`
}

// Silence is SilentFor as a duration (Validate checked it).
func (t Timer) Silence() time.Duration {
	d, _ := time.ParseDuration(t.SilentFor)
	return d
}

// Taskforce makes a template callable as a temporary team that reports to its caller.
type Taskforce struct {
	IdleFor string `yaml:"idle_for" json:"idle_for"`
}

type Role struct {
	Description      string   `yaml:"description" json:"description"`
	Instructions     string   `yaml:"instructions" json:"instructions"`
	InstructionsFile string   `yaml:"instructions_file" json:"-"`
	Tools            []string `yaml:"tools" json:"tools"`
	CanSpawn         []string `yaml:"can_spawn" json:"can_spawn"`
	// CanCallTaskforce names taskforce templates this role may call up although it is not its
	// team's gate. A solo session or a gate may call any taskforce.
	CanCallTaskforce []string `yaml:"can_call_taskforce" json:"can_call_taskforce"`
	CanSetCwd        bool     `yaml:"can_set_cwd" json:"can_set_cwd"`
	CanPin           bool     `yaml:"can_pin" json:"can_pin"` // may pin to the team board
	Spawn            Spawn    `yaml:"spawn" json:"spawn"`
}

// Spawn is how a role's workers start. "" and "inherit" leave a choice to the next link of the
// chain: for the harness the founding session's, then the daemon's default; for model and
// thinking the harness profile's, then the harness's own default.
type Spawn struct {
	Harness  string `yaml:"harness" json:"harness"`
	Model    string `yaml:"model" json:"model"`
	Thinking string `yaml:"thinking" json:"thinking"` // in the harness's own levels
	// AllowTools are native tools the harness profile turns off that this role keeps.
	AllowTools []string `yaml:"allow_tools" json:"allow_tools"`
}

// Route is one routing rule. The first rule matching a sender's and a receiver's roles decides;
// none matching denies. Cc roles get a copy of every mail the rule lets through.
type Route struct {
	From  string   `yaml:"from" json:"from"`
	To    string   `yaml:"to" json:"to"`
	Allow *bool    `yaml:"allow" json:"allow,omitempty"` // left out: true
	Cc    []string `yaml:"cc" json:"cc,omitempty"`
}

// Allowed is what the rule decides.
func (r Route) Allowed() bool { return r.Allow == nil || *r.Allow }

// Limits of a team; each is a number, or none (left out or 0: no limit).
type Limits struct {
	Depth       Limit `yaml:"depth" json:"depth"`             // how deep spawn chains may go
	Concurrency Limit `yaml:"concurrency" json:"concurrency"` // live workers at once (parked ones count)
	// MessagesPerMinute: over it a member's mail is held until an admin releases it.
	MessagesPerMinute Limit `yaml:"messages_per_participant_per_minute" json:"messages_per_participant_per_minute"`
	// MaxRespawnPerHour: a worker resumed that often in an hour is parked, and its lead told.
	MaxRespawnPerHour Limit `yaml:"max_respawn_per_hour" json:"max_respawn_per_hour"`
}

// Limit is a limit's value: 0 is none.
type Limit int

// UnmarshalYAML takes a non-negative number or none.
func (l *Limit) UnmarshalYAML(n *yaml.Node) error {
	if n.Value == "none" {
		*l = 0
		return nil
	}
	var i int
	if err := n.Decode(&i); err != nil || i < 0 {
		return fmt.Errorf("line %d: a limit is a number or none, not %q", n.Line, n.Value)
	}
	*l = Limit(i)
	return nil
}

// Parse reads a manifest; read resolves instructions_file relative to the template.
func Parse(text []byte, read func(name string) ([]byte, error)) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(text)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	for name, r := range m.Roles {
		if r == nil {
			return nil, fmt.Errorf("manifest: role %s is empty", name)
		}
		if r.InstructionsFile != "" {
			b, err := read(r.InstructionsFile)
			if err != nil {
				return nil, fmt.Errorf("manifest: role %s: instructions_file: %w", name, err)
			}
			r.Instructions = strings.TrimSpace(r.Instructions + "\n\n" + string(b))
		}
	}
	return &m, m.Validate()
}

// Validate checks that every name a manifest uses is a role it has.
func (m *Manifest) Validate() error {
	if m.Template == "" {
		return errors.New("manifest: template is empty")
	}
	if len(m.Roles) == 0 {
		return errors.New("manifest: no roles")
	}
	if m.SessionRole() == "" {
		return errors.New("manifest: auto_join_role is needed when there is more than one role")
	}
	if _, ok := m.Roles[m.SessionRole()]; !ok {
		return fmt.Errorf("manifest: auto_join_role %q is not a role", m.AutoJoinRole)
	}
	for name, r := range m.Roles {
		if len(r.CanSpawn) > 0 && (m.Limits.Depth == 0 || m.Limits.Concurrency == 0) {
			return fmt.Errorf("manifest: role %s can spawn, so limits.depth and limits.concurrency are needed", name)
		}
		for _, t := range r.Tools {
			if !slices.Contains(knownTools, t) {
				return fmt.Errorf("manifest: role %s: unknown tool %q (known: %s)", name, t, strings.Join(knownTools, ", "))
			}
		}
		for _, s := range r.CanSpawn {
			if _, ok := m.Roles[s]; !ok {
				return fmt.Errorf("manifest: role %s: can_spawn names %q, not a role", name, s)
			}
		}
	}
	for i, tm := range m.Timers {
		if _, ok := m.Roles[tm.On]; !ok {
			return fmt.Errorf("manifest: timers[%d]: on %q is not a role", i, tm.On)
		}
		if _, ok := m.Roles[tm.Notify]; !ok && tm.Notify != "reports_to" {
			return fmt.Errorf("manifest: timers[%d]: notify %q is not a role or reports_to", i, tm.Notify)
		}
		if d, err := time.ParseDuration(tm.SilentFor); err != nil || d <= 0 {
			return fmt.Errorf("manifest: timers[%d]: silent_for must be a positive duration like 20m, not %q", i, tm.SilentFor)
		}
	}
	if m.Taskforce != nil && m.Taskforce.IdleFor != "" {
		if d, err := time.ParseDuration(m.Taskforce.IdleFor); err != nil || d <= 0 {
			return fmt.Errorf("manifest: taskforce.idle_for must be a positive duration like 20m, not %q", m.Taskforce.IdleFor)
		}
	}
	for _, rt := range m.Routing {
		for _, n := range append([]string{rt.From, rt.To}, rt.Cc...) {
			if _, ok := m.Roles[n]; !ok {
				return fmt.Errorf("manifest: routing names %q, not a role", n)
			}
		}
	}
	return nil
}

// SessionRole is the role of the session that founds a team (or the chair of a taskforce).
func (m *Manifest) SessionRole() string {
	if m.AutoJoinRole != "" {
		return m.AutoJoinRole
	}
	if len(m.Roles) == 1 {
		for name := range m.Roles {
			return name
		}
	}
	return ""
}

// Allows says whether a member of role from may write to a member of role to.
func (m *Manifest) Allows(from, to string) bool {
	i := m.RuleIndex(from, to)
	return i >= 0 && m.Routing[i].Allowed()
}

// RuleIndex is the index of the first routing rule matching from and to, -1 when none does.
func (m *Manifest) RuleIndex(from, to string) int {
	for i, r := range m.Routing {
		if r.From == from && r.To == to {
			return i
		}
	}
	return -1
}

// Cc are the roles that get a copy of mail from role from to role to.
func (m *Manifest) Cc(from, to string) []string {
	if m.Allows(from, to) {
		return m.Routing[m.RuleIndex(from, to)].Cc
	}
	return nil
}

// Targets are the roles a role may write to.
func (m *Manifest) Targets(from string) []string {
	var out []string
	for _, r := range m.Routing {
		if r.From == from && !slices.Contains(out, r.To) && m.Allows(from, r.To) {
			out = append(out, r.To)
		}
	}
	return out
}

// HasTool says whether role has tool.
func (m *Manifest) HasTool(role, tool string) bool {
	r := m.Roles[role]
	return r != nil && slices.Contains(r.Tools, tool)
}

// Info is one template as `template list` and the agent tool show it.
type Info struct {
	Name      string `json:"name"`
	Origin    string `json:"origin"` // built-in | yours
	Taskforce bool   `json:"taskforce"`
	Summary   string `json:"summary"`
}

// Load reads template name: ~/.squad/templates/<name>/manifest.yaml, else the built-in one.
func Load(userDir, name string) (*Manifest, error) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return nil, fmt.Errorf("no template %q", name)
	}
	dir := filepath.Join(userDir, name)
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.yaml")); err == nil {
		return Parse(b, func(f string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, filepath.FromSlash(f))) })
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b, err := fs.ReadFile(FS, name+".yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no template %q (squad template list shows them)", name)
	}
	if err != nil {
		return nil, err
	}
	return Parse(b, builtinFile)
}

// List is every template: the user's first, then the built-ins they do not shadow.
func List(userDir string) ([]Info, error) {
	seen := map[string]bool{}
	var out []Info
	entries, _ := os.ReadDir(userDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := Load(userDir, e.Name())
		if err != nil {
			out = append(out, Info{Name: e.Name(), Origin: "yours", Summary: "error: " + err.Error()})
			seen[e.Name()] = true
			continue
		}
		out = append(out, Info{Name: e.Name(), Origin: "yours", Taskforce: m.Taskforce != nil, Summary: m.Summary})
		seen[e.Name()] = true
	}
	files, err := fs.Glob(FS, "*.yaml")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		name := strings.TrimSuffix(f, ".yaml")
		if seen[name] {
			continue
		}
		m, err := Load(userDir, name)
		if err != nil {
			return nil, fmt.Errorf("built-in %s: %w", name, err)
		}
		out = append(out, Info{Name: name, Origin: "built-in", Taskforce: m.Taskforce != nil, Summary: m.Summary})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CopyBuiltin writes built-in template from into userDir/<to> for the user to edit.
func CopyBuiltin(userDir, from, to string) (string, error) {
	b, err := fs.ReadFile(FS, from+".yaml")
	if err != nil {
		return "", fmt.Errorf("no built-in template %q", from)
	}
	dir := filepath.Join(userDir, to)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s already exists", dir)
	}
	m, err := Parse(b, builtinFile)
	if err != nil {
		return "", err
	}
	// Copy every prompt file the manifest names, at the same relative path.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, r := range m.Roles {
		if r.InstructionsFile == "" {
			continue
		}
		pb, err := builtinFile(r.InstructionsFile)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, filepath.FromSlash(r.InstructionsFile))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, pb, 0o600); err != nil {
			return "", err
		}
	}
	text := strings.Replace(string(b), "template: "+m.Template, "template: "+to, 1)
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(text), 0o600); err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.yaml"), nil
}

// builtinFile reads a file a built-in template names.
func builtinFile(f string) ([]byte, error) { return fs.ReadFile(FS, path.Clean(f)) }
