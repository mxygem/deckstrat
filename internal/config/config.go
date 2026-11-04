// Package config loads deckstrat settings from an optional JSON file and
// DECKSTRAT_* environment variables. Environment variables win.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	koanf "github.com/knadh/koanf/v2"
)

const (
	// DefaultFile is read from the working directory when present.
	DefaultFile = ".env.json"
	envPrefix   = "DECKSTRAT_"
)

type Config struct {
	// Env selects the section of the config file, e.g. "development".
	Env string `koanf:"-"`
	// JustTCGAPIKey authenticates JustTCG price requests.
	JustTCGAPIKey string `koanf:"just_tcg_api_key" json:"-"`
	// CacheDir holds cached catalog responses.
	CacheDir string `koanf:"cache_dir"`
}

// Options let callers and tests override where config comes from.
type Options struct {
	// File is the config file path. Empty means DECKSTRAT_CONFIG, then DefaultFile.
	File string
	// Environ returns the environment as KEY=value pairs. Nil means os.Environ.
	Environ func() []string
}

// Load reads config. A missing config file is not an error.
//
// The file holds one object per environment:
//
//	{"development": {"just_tcg_api_key": "..."}}
//
// Each key can be overridden by an upper-cased env var, e.g.
// DECKSTRAT_JUST_TCG_API_KEY. DECKSTRAT_ENV (or ENVIRONMENT) picks the
// section and defaults to "development".
func Load(opts Options) (*Config, error) {
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	vars := envMap(environ())

	envName := firstNonEmpty(vars[envPrefix+"ENV"], vars["ENVIRONMENT"], "development")
	path := firstNonEmpty(opts.File, vars[envPrefix+"CONFIG"], DefaultFile)

	k := koanf.New(".")
	if err := k.Load(file.Provider(path), json.Parser()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}

	// Env vars land in the selected section so they override file values.
	err := k.Load(env.Provider(".", env.Opt{
		Prefix:      envPrefix,
		EnvironFunc: environ,
		TransformFunc: func(key, v string) (string, any) {
			key = strings.ToLower(strings.TrimPrefix(key, envPrefix))
			if key == "env" || key == "config" {
				return "", nil
			}
			return envName + "." + key, v
		},
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("loading env: %w", err)
	}

	cfg := &Config{}
	if err := k.Unmarshal(envName, cfg); err != nil {
		return nil, fmt.Errorf("reading %q config: %w", envName, err)
	}
	cfg.Env = envName

	if cfg.CacheDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("finding cache dir: %w", err)
		}
		cfg.CacheDir = filepath.Join(dir, "deckstrat")
	}
	return cfg, nil
}

// RequireJustTCG reports a helpful error when the JustTCG key is missing.
func (c *Config) RequireJustTCG() error {
	if c.JustTCGAPIKey == "" {
		return fmt.Errorf("no JustTCG API key: set just_tcg_api_key under %q in %s or %sJUST_TCG_API_KEY", c.Env, DefaultFile, envPrefix)
	}
	return nil
}

// String redacts secrets so a Config is safe to log with %v and %+v.
func (c Config) String() string {
	key := "<unset>"
	if c.JustTCGAPIKey != "" {
		key = "<redacted>"
	}
	return fmt.Sprintf("Config{Env:%s JustTCGAPIKey:%s CacheDir:%s}", c.Env, key, c.CacheDir)
}

func envMap(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if k, v, ok := strings.Cut(p, "="); ok {
			m[k] = v
		}
	}
	return m
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// GoString redacts secrets for %#v.
func (c Config) GoString() string { return c.String() }
