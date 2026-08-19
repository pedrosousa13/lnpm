package tests

import (
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/workspace"
)

func TestDetectTurborepo(t *testing.T) {
	root := filepath.Join("fixtures", "turborepo")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}
	if ws.Type != "npm" && ws.Type != "yarn" {
		t.Errorf("Expected npm or yarn, got %s", ws.Type)
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 3 { // ui, utils, web
		t.Errorf("Expected 3 packages, got %d", len(packages))
	}
}

func TestDetectPNPMWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "pnpm-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}
	if ws.Type != "pnpm" {
		t.Errorf("Expected pnpm, got %s", ws.Type)
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 3 { // lib-a, lib-b, app1
		t.Errorf("Expected 3 packages, got %d", len(packages))
	}

	// Verify package names
	names := make(map[string]bool)
	for _, pkg := range packages {
		names[pkg.Name] = true
	}
	if !names["@pnpm-test/lib-a"] || !names["@pnpm-test/lib-b"] {
		t.Error("Expected @pnpm-test/lib-a and @pnpm-test/lib-b")
	}
}

func TestDetectNPMWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "npm-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(packages))
	}
}

func TestDetectNPMWorkspaceWithNegation(t *testing.T) {
	root := filepath.Join("fixtures", "npm-workspace-negation")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("Expected 1 package, got %d: %v", len(packages), packages)
	}
	if packages[0].Name != "@npm-test/package-a" {
		t.Errorf("Expected @npm-test/package-a, got %s", packages[0].Name)
	}
}

func TestDetectYarnWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "yarn-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(packages))
	}
}

func TestNoWorkspace(t *testing.T) {
	ws, err := workspace.Detect("/tmp")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if ws != nil {
		t.Error("Expected nil workspace for /tmp")
	}
}
