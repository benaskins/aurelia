package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API authentication tokens",
}

var tokenRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Rotate the API bearer token and distribute to peers",
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")

		path := "/v1/token/rotate"
		if force {
			path += "?force=true"
		}

		result, err := apiPost(path)
		if err != nil {
			return fmt.Errorf("token rotation failed: %w", err)
		}

		status, _ := result["status"].(string)
		confirmed, _ := result["confirmed"].(float64)
		total, _ := result["total"].(float64)

		switch status {
		case "rotated":
			fmt.Printf("Token rotated successfully (%d/%d peers confirmed)\n", int(confirmed), int(total))
		case "partial":
			fmt.Printf("Token rotated but %d/%d peers unreachable (old token still valid)\n",
				int(total)-int(confirmed), int(total))
			if msg, ok := result["message"].(string); ok {
				fmt.Println(msg)
			}
		}

		if peers, ok := result["peers"].(map[string]any); ok && len(peers) > 0 {
			for name, status := range peers {
				fmt.Printf("  %s: %s\n", name, status)
			}
		}

		return nil
	},
}

func init() {
	tokenRotateCmd.Flags().Bool("force", false, "Invalidate old token immediately regardless of peer confirmation")
	tokenCmd.AddCommand(tokenRotateCmd)
	rootCmd.AddCommand(tokenCmd)
}
