package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/pkieltyka/todos/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"
)

// Validate documentation against the real parser, without executing examples.
// This includes read/write examples, help, and human-only TUI entry points.
func skillContract(text string, newRoot func() *cobra.Command) []error {
	problems := []error{}
	if !strings.HasPrefix(text, "---\n") {
		return []error{fmt.Errorf("skill needs YAML front matter")}
	}
	header, _, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !ok {
		return []error{fmt.Errorf("unterminated front matter")}
	}
	var meta struct {
		Name        string
		Description string
	}
	if e := yaml.Unmarshal([]byte(header), &meta); e != nil || meta.Name != "todos" || strings.TrimSpace(meta.Description) == "" {
		problems = append(problems, fmt.Errorf("invalid skill name/description: %v", e))
	}
	root := newRoot()
	root.InitDefaultHelpCmd()
	knownFlags := map[string]bool{}
	commands := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		commands[c.CommandPath()] = true
		visit := func(f *pflag.Flag) { knownFlags[f.Name] = true }
		c.Flags().VisitAll(visit)
		c.PersistentFlags().VisitAll(visit)
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	mentionedFlags := map[string]bool{}
	for _, flag := range regexp.MustCompile(`--([a-z][a-z0-9-]*)`).FindAllStringSubmatch(text, -1) {
		mentionedFlags[flag[1]] = true
		if !knownFlags[flag[1]] {
			problems = append(problems, fmt.Errorf("skill mentions unknown flag --%s", flag[1]))
		}
	}
	for flag := range knownFlags {
		if !mentionedFlags[flag] {
			problems = append(problems, fmt.Errorf("skill omits flag --%s", flag))
		}
	}
	covered := map[string]bool{}
	shell := false
	examples := 0
	for lineNumber, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			if shell {
				shell = false
			} else {
				shell = line == "```sh" || line == "```bash"
			}
			continue
		}
		if !shell || strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		words, e := shlex.Split(line)
		if e != nil {
			problems = append(problems, fmt.Errorf("line %d: %v", lineNumber+1, e))
			continue
		}
		if len(words) == 0 {
			continue
		}
		if words[0] != "todos" {
			problems = append(problems, fmt.Errorf("line %d: expected a todos example", lineNumber+1))
			continue
		}
		examples++
		tree := newRoot()
		tree.InitDefaultHelpCmd()
		cmd, remaining, e := tree.Find(words[1:])
		if e != nil {
			problems = append(problems, fmt.Errorf("line %d: %v", lineNumber+1, e))
			continue
		}
		cmd.InitDefaultHelpFlag()
		if e := cmd.ParseFlags(remaining); e != nil {
			problems = append(problems, fmt.Errorf("line %d: %s: %v", lineNumber+1, cmd.CommandPath(), e))
			continue
		}
		if e := cmd.ValidateArgs(cmd.Flags().Args()); e != nil {
			problems = append(problems, fmt.Errorf("line %d: %s: %v", lineNumber+1, cmd.CommandPath(), e))
			continue
		}
		for c := cmd; c != nil; c = c.Parent() {
			covered[c.CommandPath()] = true
		}
	}
	if examples == 0 {
		problems = append(problems, fmt.Errorf("skill has no shell examples"))
	}
	for command := range commands {
		if !covered[command] {
			problems = append(problems, fmt.Errorf("skill lacks an example for %s", command))
		}
	}
	return problems
}
func skillRoot() *cobra.Command { return cli.NewRoot(cli.Dependencies{}) }
func TestSkillMatchesCLI(t *testing.T) {
	for _, e := range skillContract(string(skill), skillRoot) {
		t.Error(e)
	}
}
func TestSkillContract_RejectsUnknownCommandsAndFlags(t *testing.T) {
	for _, text := range []string{strings.Replace(string(skill), "todos list --state", "todos imaginary --state", 1), strings.Replace(string(skill), "todos view 12 --json", "todos view 12 --checked close --json", 1), string(skill) + "\nAn invented flag: --imaginary.\n", strings.Replace(string(skill), "todos pr view 23 --json\n", "", 1)} {
		if problems := skillContract(text, skillRoot); len(problems) == 0 {
			t.Fatal("broken documentation passed")
		}
	}
}
func TestSkillContract_NewCLICommandNeedsAnExample(t *testing.T) {
	factory := func() *cobra.Command {
		root := skillRoot()
		root.AddCommand(&cobra.Command{Use: "future-command", Run: func(*cobra.Command, []string) { t.Fatal("example was executed") }})
		return root
	}
	if len(skillContract(string(skill), factory)) == 0 {
		t.Fatal("undocumented command passed")
	}
}
