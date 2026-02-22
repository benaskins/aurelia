package main

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/benaskins/aurelia/skills"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

var skillsCmd = &cobra.Command{
	Use:   "skills [name]",
	Short: "List or show aurelia skills",
	Long:  "List available skills or show the full content of a specific skill.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkills,
}

func init() {
	rootCmd.AddCommand(skillsCmd)
}

func runSkills(cmd *cobra.Command, args []string) error {
	entries, err := loadSkills()
	if err != nil {
		return fmt.Errorf("loading skills: %w", err)
	}

	if len(args) == 0 {
		return listSkills(entries)
	}
	return showSkill(entries, args[0])
}

type skillEntry struct {
	frontmatter skillFrontmatter
	content     string
}

func loadSkills() (map[string]skillEntry, error) {
	entries := make(map[string]skillEntry)

	err := fs.WalkDir(skills.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}

		data, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		raw := string(data)
		fm, err := parseFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("parsing frontmatter in %s: %w", path, err)
		}

		entries[fm.Name] = skillEntry{
			frontmatter: fm,
			content:     raw,
		}
		return nil
	})

	return entries, err
}

func parseFrontmatter(content string) (skillFrontmatter, error) {
	// Frontmatter is between first and second "---" delimiters.
	const delimiter = "---"
	rest := content

	idx := strings.Index(rest, delimiter)
	if idx < 0 {
		return skillFrontmatter{}, fmt.Errorf("no opening frontmatter delimiter")
	}
	rest = rest[idx+len(delimiter):]

	idx = strings.Index(rest, delimiter)
	if idx < 0 {
		return skillFrontmatter{}, fmt.Errorf("no closing frontmatter delimiter")
	}
	block := rest[:idx]

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return skillFrontmatter{}, fmt.Errorf("unmarshalling YAML: %w", err)
	}
	return fm, nil
}

func listSkills(entries map[string]skillEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no skills found")
		return nil
	}

	for _, e := range entries {
		fmt.Printf("%-20s %s\n", e.frontmatter.Name, e.frontmatter.Description)
	}
	return nil
}

func showSkill(entries map[string]skillEntry, name string) error {
	e, ok := entries[name]
	if !ok {
		available := make([]string, 0, len(entries))
		for k := range entries {
			available = append(available, k)
		}
		return fmt.Errorf("unknown skill %q (available: %s)", name, strings.Join(available, ", "))
	}

	fmt.Print(e.content)
	return nil
}
