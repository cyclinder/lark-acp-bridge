// Package preflight checks that a local agent binary is installed and
// responsive before the bridge tries to use it.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CheckResult describes a preflight probe outcome.
type CheckResult struct {
	Available bool
	Version   string
	Error     error
}

// CheckDevin runs `devin --version` with a short timeout.
func CheckDevin(ctx context.Context, binary string) CheckResult {
	if binary == "" {
		binary = "devin"
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, binary, "--version").Output()
	if err != nil {
		return CheckResult{Error: fmt.Errorf("devin not available: %w", err)}
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return CheckResult{Error: errors.New("devin --version returned empty output")}
	}
	return CheckResult{Available: true, Version: version}
}
