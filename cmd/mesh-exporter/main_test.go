package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	// Build the binary, then run it with --version.
	// This tests the real flag parsing path without hitting os.Exit in-process.
	bin := t.TempDir() + "/mesh-exporter-test"
	build := exec.Command("go", "build", "-ldflags", "-X main.version=test-v1.2.3", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "--version")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("--version failed: %v", err)
	}

	got := strings.TrimSpace(string(out))
	if got != "test-v1.2.3" {
		t.Errorf("version output = %q, want %q", got, "test-v1.2.3")
	}
}

func TestVersionFlagDefault(t *testing.T) {
	bin := t.TempDir() + "/mesh-exporter-test"
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "--version")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("--version failed: %v", err)
	}

	got := strings.TrimSpace(string(out))
	if got != "dev" {
		t.Errorf("default version = %q, want %q", got, "dev")
	}
}
