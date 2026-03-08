package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// resolveRepoRoot walks from dir upward (max 3 levels) looking for the repo root.
// The repo root is identified by the presence of refresh.sh — the script that
// generates data.json and is unique to the dashboard repo root.
// This allows binaries built into dist/ (or other subdirs) to locate assets like
// refresh.sh, config.json, data.json, and VERSION.
// Returns dir unchanged if no parent with refresh.sh is found (binary IS at repo root).
func resolveRepoRoot(dir string) string {
	// If refresh.sh exists in dir, this IS the repo root
	if _, err := os.Stat(filepath.Join(dir, "refresh.sh")); err == nil {
		return dir
	}
	// Walk up to 3 parent directories looking for refresh.sh
	candidate := dir
	for i := 0; i < 3; i++ {
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break // reached filesystem root
		}
		candidate = parent
		if _, err := os.Stat(filepath.Join(candidate, "refresh.sh")); err == nil {
			return candidate
		}
	}
	// No repo root found — return original dir (best-effort)
	return dir
}

func detectVersion(dir string) string {
	// 1. VERSION file — allow worktrees/experimental builds to override tagged releases.
	// Check both the executable directory and its parent so binaries built into ./dist
	// still pick up the repo-root VERSION file.
	for _, base := range []string{dir, filepath.Dir(dir)} {
		vf := filepath.Join(base, "VERSION")
		data, err := os.ReadFile(vf)
		if err == nil {
			v := strings.TrimSpace(string(data))
			if v != "" {
				return strings.TrimPrefix(v, "v")
			}
		}
	}
	// 2. git describe --tags --abbrev=0 — with 5s timeout (parity with server.py)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--abbrev=0")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		tag := strings.TrimSpace(string(out))
		if tag != "" {
			return strings.TrimPrefix(tag, "v")
		}
	}
	// 3. fallback
	return "dev"
}
