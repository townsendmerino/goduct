package cliconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_AllFields(t *testing.T) {
	raw := []byte(`{
		"pattern": "./api",
		"out": "./web/src/api",
		"dir": ".",
		"tags": ["integration"],
		"tests": false,
		"watch": false,
		"framework": "gin",
		"all": false,
		"generators": ["types", "zod", "client"],
		"adapters": {
			"github.com/shopspring/decimal.Decimal": "string"
		},
		"openapi": {
			"title": "My API",
			"version": "1.2.3",
			"description": "Hi.",
			"servers": ["https://api.example.com"]
		}
	}`)
	cfg, err := Parse(raw, "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Pattern == nil || *cfg.Pattern != "./api" {
		t.Errorf("Pattern = %v", cfg.Pattern)
	}
	if cfg.Out == nil || *cfg.Out != "./web/src/api" {
		t.Errorf("Out = %v", cfg.Out)
	}
	if cfg.Framework == nil || *cfg.Framework != "gin" {
		t.Errorf("Framework = %v", cfg.Framework)
	}
	if got := cfg.Generators; len(got) != 3 || got[0] != "types" || got[2] != "client" {
		t.Errorf("Generators = %v", got)
	}
	if cfg.Adapters["github.com/shopspring/decimal.Decimal"] != "string" {
		t.Errorf("Adapters = %v", cfg.Adapters)
	}
	if cfg.OpenAPI == nil {
		t.Fatal("OpenAPI nil")
	}
	if cfg.OpenAPI.Title != "My API" {
		t.Errorf("OpenAPI.Title = %q", cfg.OpenAPI.Title)
	}
	if cfg.OpenAPI.Version != "1.2.3" {
		t.Errorf("OpenAPI.Version = %q", cfg.OpenAPI.Version)
	}
	if len(cfg.OpenAPI.Servers) != 1 || cfg.OpenAPI.Servers[0] != "https://api.example.com" {
		t.Errorf("OpenAPI.Servers = %v", cfg.OpenAPI.Servers)
	}
}

func TestParse_DistinguishesAbsentFromFalse(t *testing.T) {
	// Pointer fields make this distinction possible: "tests" explicitly
	// false ≠ "tests" absent. The precedence overlay depends on this.
	absent, err := Parse([]byte(`{"pattern":"./api"}`), "t.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if absent.Tests != nil {
		t.Errorf("absent Tests should be nil, got %v", absent.Tests)
	}
	if absent.Watch != nil {
		t.Errorf("absent Watch should be nil, got %v", absent.Watch)
	}

	explicit, err := Parse([]byte(`{"tests":false,"watch":true}`), "t.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if explicit.Tests == nil || *explicit.Tests != false {
		t.Errorf("explicit Tests should be *false, got %v", explicit.Tests)
	}
	if explicit.Watch == nil || *explicit.Watch != true {
		t.Errorf("explicit Watch should be *true, got %v", explicit.Watch)
	}
}

func TestParse_UnknownFieldLoudFails(t *testing.T) {
	// ADR 0007: a typo'd key is a loud-fail, not a silent ignore.
	raw := []byte(`{"frameworks":"chi"}`) // typo: should be "framework"
	_, err := Parse(raw, "test.json")
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "frameworks") {
		t.Errorf("error should name the unknown field, got: %v", err)
	}
}

func TestParse_TrailingDataRejected(t *testing.T) {
	raw := []byte(`{"pattern":"./api"}{"extra":true}`)
	_, err := Parse(raw, "test.json")
	if err == nil {
		t.Fatal("expected error for trailing data, got nil")
	}
	if !strings.Contains(err.Error(), "trailing") {
		t.Errorf("error should mention 'trailing', got: %v", err)
	}
}

func TestLoad_ExplicitPathMissingErrors(t *testing.T) {
	// An explicit --config <path> that doesn't exist is an error
	// (per ADR 0038 §2: a missing config the user named explicitly
	// is never a silent no-op).
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Fatal("expected error for missing explicit path, got nil")
	}
}

func TestLoad_AutoDiscoverMissingReturnsNilNil(t *testing.T) {
	// Auto-discovery (empty --config) silently returns (nil, nil)
	// when goduct.json is absent — the CLI then runs flag-only.
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != nil {
		t.Errorf("Load(\"\") in empty dir should return nil cfg, got %+v", cfg)
	}
}

func TestLoad_AutoDiscoverPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultFilename),
		[]byte(`{"pattern":"./api","framework":"gin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil || cfg.Pattern == nil || *cfg.Pattern != "./api" {
		t.Errorf("Load(\"\") with present file should yield cfg, got %+v", cfg)
	}
}
