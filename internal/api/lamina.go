package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// allowedCommands is the exhaustive set of lamina subcommands that can be
// executed remotely. Anything not in this set is rejected.
var allowedCommands = map[string]bool{
	"completion": true,
	"deps":       true,
	"doctor":     true,
	"eval":       true,
	"heal":       true,
	"help":       true,
	"hooks":      true,
	"init":       true,
	"release":    true,
	"repo":       true,
	"skills":     true,
	"test":       true,
}

// laminaRequest is the JSON body for POST /v1/lamina.
type laminaRequest struct {
	Args []string `json:"args"` // e.g. ["repo", "fetch"]
}

// laminaResponse is the JSON response from POST /v1/lamina.
type laminaResponse struct {
	ExitCode int             `json:"exit_code"`
	Output   json.RawMessage `json:"output,omitempty"` // parsed JSON if --json output
	Raw      string          `json:"raw,omitempty"`    // raw text if not valid JSON
	Error    string          `json:"error,omitempty"`
}

const laminaExecTimeout = 5 * time.Minute

func (s *Server) laminaExec(w http.ResponseWriter, r *http.Request) {
	if s.laminaRoot == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "lamina_root not configured",
		})
		return
	}

	var req laminaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	if len(req.Args) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "args required (e.g. [\"repo\", \"status\"])",
		})
		return
	}

	// Validate subcommand against allowlist
	subcmd := req.Args[0]
	if !allowedCommands[subcmd] {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": fmt.Sprintf("command %q not allowed", subcmd),
		})
		return
	}

	// Resolve lamina binary — check workspace bin/, ~/.local/bin/, then PATH
	laminaBin := resolveLaminaBin(s.laminaRoot)

	// Build command: lamina --json <args...>
	// Always inject --json for structured output
	cmdArgs := append([]string{"--json"}, req.Args...)
	ctx, cancel := context.WithTimeout(r.Context(), laminaExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, laminaBin, cmdArgs...)
	cmd.Dir = s.laminaRoot
	cmd.Env = append(cmd.Environ(), "LAMINA_ROOT="+s.laminaRoot)

	out, err := cmd.CombinedOutput()

	resp := laminaResponse{
		ExitCode: cmd.ProcessState.ExitCode(),
	}

	if err != nil && resp.ExitCode == -1 {
		// Process didn't start or was killed
		resp.Error = err.Error()
		writeJSON(w, http.StatusInternalServerError, resp)
		return
	}

	// Try to parse output as JSON; fall back to raw string
	var parsed json.RawMessage
	if json.Unmarshal(out, &parsed) == nil {
		resp.Output = parsed
	} else {
		resp.Raw = string(out)
	}

	if resp.ExitCode != 0 && resp.Error == "" {
		resp.Error = fmt.Sprintf("lamina %s exited %d", subcmd, resp.ExitCode)
	}

	status := http.StatusOK
	if resp.ExitCode != 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, resp)
}

// resolveLaminaBin finds the lamina binary by checking well-known locations
// before falling back to PATH lookup. The daemon's exec environment often
// doesn't include ~/.local/bin.
func resolveLaminaBin(workspaceRoot string) string {
	// 1. Workspace bin/ directory (just install puts it here sometimes)
	if workspaceRoot != "" {
		if p := filepath.Join(workspaceRoot, "bin", "lamina"); fileExists(p) {
			return p
		}
	}

	// 2. ~/.local/bin/lamina (standard install location)
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", "lamina"); fileExists(p) {
			return p
		}
	}

	// 3. Fall back to PATH
	if p, err := exec.LookPath("lamina"); err == nil {
		return p
	}

	return "lamina" // let exec fail with a clear error
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
