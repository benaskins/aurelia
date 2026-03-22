package daemon

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// ShipStep records the result of one step in the ship pipeline.
type ShipStep struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "ok" | "failed" | "skipped"
	Output   string `json:"output,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// ShipResult is the outcome of a full ship pipeline.
type ShipResult struct {
	Service string     `json:"service"`
	Steps   []ShipStep `json:"steps"`
	Success bool       `json:"success"`
}

// ShipService runs the fetch → build → deploy → notify pipeline for a service.
func (d *Daemon) ShipService(name string) (*ShipResult, error) {
	ms, err := d.getService(name)
	if err != nil {
		return nil, err
	}

	src := ms.spec.Service.Source
	if src == nil {
		return nil, fmt.Errorf("service %q has no source config — add source.repo and source.build to the service spec", name)
	}

	result := &ShipResult{Service: name, Success: true}

	// Step 1: Fetch
	step := runStep("fetch", src.Repo, "git pull --rebase")
	result.Steps = append(result.Steps, step)
	if step.Status == "failed" {
		result.Success = false
		return result, nil
	}

	// Step 2: Build
	step = runStep("build", src.Repo, src.Build)
	result.Steps = append(result.Steps, step)
	if step.Status == "failed" {
		result.Success = false
		return result, nil
	}

	// Step 3: Deploy
	start := time.Now()
	deployErr := d.DeployService(name, 5*time.Second)
	dur := time.Since(start).Truncate(time.Millisecond).String()
	if deployErr != nil {
		result.Steps = append(result.Steps, ShipStep{
			Name:     "deploy",
			Status:   "failed",
			Output:   deployErr.Error(),
			Duration: dur,
		})
		result.Success = false
		return result, nil
	}
	result.Steps = append(result.Steps, ShipStep{
		Name:     "deploy",
		Status:   "ok",
		Duration: dur,
	})

	// Step 4: Notify (best-effort)
	notifyMsg := fmt.Sprintf("Deployed %s", name)
	step = runStep("notify", "", "signal-send.sh '"+notifyMsg+"'")
	if step.Status == "failed" {
		step.Status = "skipped"
		step.Output = "signal-send.sh not available"
	}
	result.Steps = append(result.Steps, step)

	return result, nil
}

func runStep(name, dir, command string) ShipStep {
	slog.Info("ship: running step", "step", name, "command", command, "dir", dir)
	start := time.Now()

	cmd := exec.Command("sh", "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	dur := time.Since(start).Truncate(time.Millisecond).String()

	output := strings.TrimSpace(string(out))
	if err != nil {
		slog.Warn("ship: step failed", "step", name, "error", err, "output", output)
		return ShipStep{Name: name, Status: "failed", Output: output, Duration: dur}
	}

	slog.Info("ship: step complete", "step", name, "duration", dur)
	return ShipStep{Name: name, Status: "ok", Output: output, Duration: dur}
}
