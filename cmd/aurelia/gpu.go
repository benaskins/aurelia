package main

import (
	"fmt"

	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/spf13/cobra"
)

var gpuCmd = &cobra.Command{
	Use:   "gpu",
	Short: "Show GPU status",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		info := gpu.QueryNow()

		if jsonOut {
			return printJSON(info)
		}

		fmt.Printf("GPU:              %s\n", info.Name)
		if info.HasUnifiedMemory {
			fmt.Printf("Memory:           Unified\n")
		}
		fmt.Printf("Allocated:        %.1f GB\n", info.AllocatedGB())
		fmt.Printf("Recommended Max:  %.1f GB\n", info.RecommendedMaxGB())
		if info.RecommendedMax > 0 {
			fmt.Printf("Usage:            %.1f%%\n", info.UsagePercent)
		}
		fmt.Printf("Thermal:          %s\n", info.ThermalState)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(gpuCmd)
}
