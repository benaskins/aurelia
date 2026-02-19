package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/benaskins/aurelia/internal/audit"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func aureliaDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.aurelia"
	}
	return filepath.Join(home, ".aurelia")
}

func newAuditedStore() (*keychain.AuditedStore, error) {
	dir := aureliaDir()
	os.MkdirAll(dir, 0755)

	auditLog, err := audit.NewLogger(filepath.Join(dir, "audit.log"))
	if err != nil {
		return nil, err
	}

	meta, err := keychain.NewMetadataStore(filepath.Join(dir, "secret-metadata.json"))
	if err != nil {
		return nil, err
	}

	inner := keychain.NewKeychainStore()
	return keychain.NewAuditedStore(inner, auditLog, meta, "cli"), nil
}

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage secrets in macOS Keychain",
}

var secretSetCmd = &cobra.Command{
	Use:   "set <key> [value]",
	Short: "Store a secret in the Keychain",
	Long:  "Store a secret. If value is omitted, reads from stdin (useful for piping).",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newAuditedStore()
		if err != nil {
			return err
		}
		key := args[0]

		var value string
		if len(args) == 2 {
			value = args[1]
		} else {
			if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Print("Enter secret value: ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return fmt.Errorf("reading password: %w", err)
				}
				fmt.Println()
				value = string(b)
			} else {
				b, err := os.ReadFile("/dev/stdin")
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}
				value = strings.TrimRight(string(b), "\n")
			}
		}

		if err := store.Set(key, value); err != nil {
			return err
		}
		fmt.Printf("Secret %q stored\n", key)
		return nil
	},
}

var secretGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Retrieve a secret from the Keychain",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newAuditedStore()
		if err != nil {
			return err
		}
		val, err := store.Get(args[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all secrets with age and rotation status",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newAuditedStore()
		if err != nil {
			return err
		}
		keys, err := store.List()
		if err != nil {
			return err
		}

		if len(keys) == 0 {
			fmt.Println("No secrets stored")
			return nil
		}

		allMeta := store.Metadata().All()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tAGE\tPOLICY\tSTATUS")
		for _, k := range keys {
			age := "-"
			policy := "-"
			status := "ok"

			if meta, ok := allMeta[k]; ok {
				if !meta.CreatedAt.IsZero() {
					age = formatAge(time.Since(meta.CreatedAt))
				}
				if meta.RotateEvery != "" {
					policy = meta.RotateEvery
					// Check staleness
					lastSet := meta.CreatedAt
					if !meta.LastRotated.IsZero() {
						lastSet = meta.LastRotated
					}
					if maxAge, err := parseDuration(meta.RotateEvery); err == nil {
						elapsed := time.Since(lastSet)
						if elapsed > maxAge {
							status = "STALE"
						} else if elapsed > maxAge*9/10 {
							status = "warning"
						}
					}
				}
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k, age, policy, status)
		}
		w.Flush()
		return nil
	},
}

var secretDeleteCmd = &cobra.Command{
	Use:     "delete <key>",
	Short:   "Remove a secret from the Keychain",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newAuditedStore()
		if err != nil {
			return err
		}
		if err := store.Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("Secret %q deleted\n", args[0])
		return nil
	},
}

var secretRotateCmd = &cobra.Command{
	Use:   "rotate <key>",
	Short: "Rotate a secret using its configured rotation command",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rotateCmd, _ := cmd.Flags().GetString("command")
		if rotateCmd == "" {
			return fmt.Errorf("--command is required (rotation command that outputs new value to stdout)")
		}

		store, err := newAuditedStore()
		if err != nil {
			return err
		}

		if err := store.Rotate(args[0], rotateCmd); err != nil {
			return err
		}
		fmt.Printf("Secret %q rotated\n", args[0])
		return nil
	},
}

func init() {
	secretRotateCmd.Flags().StringP("command", "c", "", "Command to generate new secret value")
	secretCmd.AddCommand(secretSetCmd)
	secretCmd.AddCommand(secretGetCmd)
	secretCmd.AddCommand(secretListCmd)
	secretCmd.AddCommand(secretDeleteCmd)
	secretCmd.AddCommand(secretRotateCmd)
	rootCmd.AddCommand(secretCmd)
}

// formatAge returns a human-readable age string.
func formatAge(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days == 0 {
		hours := int(d.Hours())
		if hours == 0 {
			return fmt.Sprintf("%dm", int(d.Minutes()))
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", days)
}

// parseDuration parses durations like "30d", "90d", "7d" into time.Duration.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
