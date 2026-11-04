package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func environ(vars ...string) func() []string {
	return func() []string { return vars }
}

func TestLoad(t *testing.T) {
	file := writeFile(t, `{
		"development": {"just_tcg_api_key": "file-dev", "cache_dir": "/tmp/dev"},
		"production":  {"just_tcg_api_key": "file-prod"}
	}`)

	tests := []struct {
		name      string
		file      string
		env       []string
		wantKey   string
		wantEnv   string
		wantCache string
	}{
		{name: "file only", file: file, wantKey: "file-dev", wantEnv: "development", wantCache: "/tmp/dev"},
		{name: "env selects section", file: file, env: []string{"DECKSTRAT_ENV=production"}, wantKey: "file-prod", wantEnv: "production"},
		{name: "ENVIRONMENT selects section", file: file, env: []string{"ENVIRONMENT=production"}, wantKey: "file-prod", wantEnv: "production"},
		{name: "env overrides file", file: file, env: []string{"DECKSTRAT_JUST_TCG_API_KEY=from-env"}, wantKey: "from-env", wantEnv: "development", wantCache: "/tmp/dev"},
		{name: "missing file, env only", file: filepath.Join(t.TempDir(), "nope.json"), env: []string{"DECKSTRAT_JUST_TCG_API_KEY=only-env"}, wantKey: "only-env", wantEnv: "development"},
		{name: "missing file, nothing set", file: filepath.Join(t.TempDir(), "nope.json"), wantEnv: "development"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(Options{File: tt.file, Environ: environ(tt.env...)})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.JustTCGAPIKey != tt.wantKey {
				t.Errorf("key = %q, want %q", cfg.JustTCGAPIKey, tt.wantKey)
			}
			if cfg.Env != tt.wantEnv {
				t.Errorf("env = %q, want %q", cfg.Env, tt.wantEnv)
			}
			if tt.wantCache != "" && cfg.CacheDir != tt.wantCache {
				t.Errorf("cache dir = %q, want %q", cfg.CacheDir, tt.wantCache)
			}
			if cfg.CacheDir == "" {
				t.Error("cache dir should default")
			}
		})
	}
}

func TestLoadConfigPathFromEnv(t *testing.T) {
	file := writeFile(t, `{"development": {"just_tcg_api_key": "via-path"}}`)
	cfg, err := Load(Options{Environ: environ("DECKSTRAT_CONFIG=" + file)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JustTCGAPIKey != "via-path" {
		t.Errorf("key = %q", cfg.JustTCGAPIKey)
	}
}

func TestLoadBadJSON(t *testing.T) {
	file := writeFile(t, `{not json`)
	if _, err := Load(Options{File: file, Environ: environ()}); err == nil {
		t.Fatal("want error for malformed file")
	}
}

func TestStringRedactsKey(t *testing.T) {
	cfg := &Config{Env: "development", JustTCGAPIKey: "tcg_secret"}
	if s := cfg.String(); strings.Contains(s, "tcg_secret") {
		t.Errorf("String leaked key: %s", s)
	}
	if err := (&Config{}).RequireJustTCG(); err == nil {
		t.Error("RequireJustTCG should fail without a key")
	}
}
