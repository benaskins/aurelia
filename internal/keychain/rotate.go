package keychain

import (
	"fmt"
	"os/exec"
	"strings"
)

// runRotationCommand executes a rotation script and captures its stdout.
// The script must output the new secret value to stdout (and only the value).
func runRotationCommand(command string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", command)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("exit code %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimRight(string(output), "\n"), nil
}
