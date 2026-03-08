package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestResolveRepoRoot_Direct(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "refresh.sh"), []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatalf("write refresh.sh: %v", err)
	}

	got := resolveRepoRoot(dir)
	if got != dir {
		t.Fatalf("expected %s, got %s", dir, got)
	}
}

func TestResolveRepoRoot_DistSubdir(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "refresh.sh"), []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatalf("write refresh.sh: %v", err)
	}
	dist := filepath.Join(repo, "dist")
	if err := os.MkdirAll(dist, 0755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}

	got := resolveRepoRoot(dist)
	if got != repo {
		t.Fatalf("expected repo root %s, got %s", repo, got)
	}
}
