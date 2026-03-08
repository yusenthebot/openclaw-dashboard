package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func TestDetectVersion_VersionFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2.5.0\n"), 0644)

	v := detectVersion(dir)

	// In a temp dir there's no git repo, so it should fall back to VERSION file
	if v != "2.5.0" {
		t.Fatalf("expected 2.5.0, got %s", v)
	}
}

func TestDetectVersion_Fallback(t *testing.T) {
	dir := t.TempDir()
	// No git, no VERSION file

	v := detectVersion(dir)

	if v != "dev" {
		t.Fatalf("expected dev, got %s", v)
	}
}

func TestDetectVersion_EmptyVersionFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("  \n"), 0644)

	v := detectVersion(dir)

	if v != "dev" {
		t.Fatalf("expected dev for empty VERSION file, got %s", v)
	}
}

func TestDetectVersion_VersionFilePrecedesGitTag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2026.3.5-beta-runtime-observability\n"), 0644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	mustRun(t, dir, "git", "init")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRun(t, dir, "git", "add", "README.md", "VERSION")
	mustRun(t, dir, "git", "commit", "-m", "init")
	mustRun(t, dir, "git", "tag", "v9999.1.1")

	v := detectVersion(dir)

	if v != "2026.3.5-beta-runtime-observability" {
		t.Fatalf("expected VERSION file to take precedence over git tag, got %s", v)
	}
}

func TestDetectVersion_ParentDirectoryVersionFile(t *testing.T) {
	repoDir := t.TempDir()
	binDir := filepath.Join(repoDir, "dist")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "VERSION"), []byte("2026.3.5-beta-runtime-observability\n"), 0644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	v := detectVersion(binDir)

	if v != "2026.3.5-beta-runtime-observability" {
		t.Fatalf("expected parent VERSION file to be used for dist binary, got %s", v)
	}
}

// ── resolveRepoRoot tests ────────────────────────────────────────────────

func TestResolveRepoRoot_RepoRootDirect(t *testing.T) {
	// When binary is at repo root (refresh.sh is in same dir), return dir unchanged
	repoDir := t.TempDir()
	os.WriteFile(filepath.Join(repoDir, "refresh.sh"), []byte("#!/bin/bash\n"), 0755)

	got := resolveRepoRoot(repoDir)
	if got != repoDir {
		t.Fatalf("expected %s, got %s", repoDir, got)
	}
}

func TestResolveRepoRoot_BinaryInDist(t *testing.T) {
	// Binary in dist/ subdir, refresh.sh in parent (repo root)
	repoDir := t.TempDir()
	distDir := filepath.Join(repoDir, "dist")
	os.MkdirAll(distDir, 0755)
	os.WriteFile(filepath.Join(repoDir, "refresh.sh"), []byte("#!/bin/bash\n"), 0755)

	got := resolveRepoRoot(distDir)
	if got != repoDir {
		t.Fatalf("expected repo root %s, got %s", repoDir, got)
	}
}

func TestResolveRepoRoot_BinaryInDeepSubdir(t *testing.T) {
	// Binary in build/output/ (2 levels deep), refresh.sh at repo root
	repoDir := t.TempDir()
	deepDir := filepath.Join(repoDir, "build", "output")
	os.MkdirAll(deepDir, 0755)
	os.WriteFile(filepath.Join(repoDir, "refresh.sh"), []byte("#!/bin/bash\n"), 0755)

	got := resolveRepoRoot(deepDir)
	if got != repoDir {
		t.Fatalf("expected repo root %s from 2 levels deep, got %s", repoDir, got)
	}
}

func TestResolveRepoRoot_NoRefreshScript(t *testing.T) {
	// No refresh.sh anywhere — returns original dir
	dir := t.TempDir()
	got := resolveRepoRoot(dir)
	if got != dir {
		t.Fatalf("expected unchanged dir %s when no refresh.sh found, got %s", dir, got)
	}
}

func TestResolveRepoRoot_TooDeep(t *testing.T) {
	// refresh.sh is 4 levels up — beyond the 3-level limit
	repoDir := t.TempDir()
	deepDir := filepath.Join(repoDir, "a", "b", "c", "d")
	os.MkdirAll(deepDir, 0755)
	os.WriteFile(filepath.Join(repoDir, "refresh.sh"), []byte("#!/bin/bash\n"), 0755)

	got := resolveRepoRoot(deepDir)
	// Should NOT find refresh.sh (4 levels up > 3 max)
	if got == repoDir {
		t.Fatalf("should not walk more than 3 levels up, but found repo root at %s", repoDir)
	}
}
