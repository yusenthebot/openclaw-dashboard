package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// resolveRepoRoot walks upward from dir (max 3 levels) looking for the
// dashboard repo root, identified by refresh.sh. This allows binaries built
// into dist/ to still find repo-root assets like refresh.sh, data.json,
// config.json, and VERSION.
func resolveRepoRoot(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "refresh.sh")); err == nil {
		return dir
	}
	candidate := dir
	for i := 0; i < 3; i++ {
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
		if _, err := os.Stat(filepath.Join(candidate, "refresh.sh")); err == nil {
			return candidate
		}
	}
	return dir
}

func detectVersion(dir string) string {
	// 1. git describe --tags --abbrev=0 — with 5s timeout (parity with server.py)
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
	// 2. VERSION file
	vf := filepath.Join(dir, "VERSION")
	data, err := os.ReadFile(vf)
	if err == nil {
		v := strings.TrimSpace(string(data))
		if v != "" {
			return v
		}
	}
	// 3. fallback
	return "dev"
}
