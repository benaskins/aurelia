package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

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
		store := keychain.NewKeychainStore()
		key := args[0]

		var value string
		if len(args) == 2 {
			value = args[1]
		} else {
			// Read from stdin
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
		store := keychain.NewKeychainStore()
		val, err := store.Get(args[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all secrets",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		store := keychain.NewKeychainStore()
		keys, err := store.List()
		if err != nil {
			return err
		}

		if len(keys) == 0 {
			fmt.Println("No secrets stored")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY")
		for _, k := range keys {
			fmt.Fprintln(w, k)
		}
		w.Flush()
		return nil
	},
}

var secretDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Remove a secret from the Keychain",
	Aliases: []string{"rm"},
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := keychain.NewKeychainStore()
		if err := store.Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("Secret %q deleted\n", args[0])
		return nil
	},
}

func init() {
	secretCmd.AddCommand(secretSetCmd)
	secretCmd.AddCommand(secretGetCmd)
	secretCmd.AddCommand(secretListCmd)
	secretCmd.AddCommand(secretDeleteCmd)
	rootCmd.AddCommand(secretCmd)
}
