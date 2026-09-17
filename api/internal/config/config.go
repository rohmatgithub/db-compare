// Package config reads server settings from environment variables.
package config

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// EnvFile is the .env file that was applied, or empty when none was.
	EnvFile string

	ListenAddr     string
	AppDatabaseURL string
	SecretKey      []byte

	// SessionTTL is how long a login stays valid.
	SessionTTL time.Duration
	// AdminUsername and AdminPassword create the first admin when the users
	// table is empty. An empty password makes the server generate one.
	AdminUsername string
	AdminPassword string

	RetentionDays     int
	MaxParallelTables int
	MaxConnsPerDB     int
	QueryTimeout      time.Duration
	HeartbeatInterval time.Duration
}

// Load reads settings from the environment. Variables from a .env file fill
// in anything the environment leaves unset or empty. The file is ENV_FILE
// when set; otherwise the nearest .env from the working directory up to the
// module root (the directory holding go.mod), so the server finds api/.env
// whether it is started from api/ or api/cmd/server.
func Load() (*Config, error) {
	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = findDotEnv()
	}
	if envFile != "" {
		if err := loadDotEnv(envFile); err != nil {
			return nil, err
		}
	}
	c := &Config{
		EnvFile:        envFile,
		ListenAddr:     env("LISTEN_ADDR", ":8080"),
		AppDatabaseURL: os.Getenv("APP_DATABASE_URL"),
		AdminUsername:  env("ADMIN_USERNAME", "admin"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
	}
	source := "no .env file found; set ENV_FILE or create api/.env"
	if envFile != "" {
		source = "env file: " + envFile
	}
	if c.AppDatabaseURL == "" {
		return nil, fmt.Errorf("APP_DATABASE_URL is required (%s)", source)
	}

	key, err := base64.StdEncoding.DecodeString(os.Getenv("APP_SECRET_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("APP_SECRET_KEY must be 32 random bytes, base64 encoded (openssl rand -base64 32)")
	}
	c.SecretKey = key

	var errs []error
	c.RetentionDays = envInt("RETENTION_DAYS", 30, &errs)
	c.MaxParallelTables = envInt("MAX_PARALLEL_TABLES", 8, &errs)
	c.MaxConnsPerDB = envInt("MAX_CONNS_PER_DB", 10, &errs)
	c.QueryTimeout = envDuration("QUERY_TIMEOUT", 30*time.Minute, &errs)
	c.HeartbeatInterval = envDuration("HEARTBEAT_INTERVAL", 10*time.Second, &errs)
	c.SessionTTL = envDuration("SESSION_TTL", 12*time.Hour, &errs)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if c.MaxConnsPerDB < 2 {
		return nil, errors.New("MAX_CONNS_PER_DB must be at least 2")
	}
	if c.SessionTTL < time.Minute {
		return nil, errors.New("SESSION_TTL must be at least 1m")
	}
	return c, nil
}

// findDotEnv returns the nearest .env between the working directory and the
// module root, or "" when there is none.
func findDotEnv() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadDotEnv applies KEY=VALUE lines from path to variables that are unset
// or empty. Blank lines, "#" comments, an "export " prefix, and quoted values
// are supported.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read env file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			switch {
			case value[0] == '"' && value[len(value)-1] == '"':
				unquoted, err := strconv.Unquote(value)
				if err != nil {
					return fmt.Errorf("%s:%d: %w", path, n, err)
				}
				value = unquoted
			case value[0] == '\'' && value[len(value)-1] == '\'':
				value = value[1 : len(value)-1]
			}
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int, errs *[]error) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", name, err))
	}
	return n
}

func envDuration(name string, fallback time.Duration, errs *[]error) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", name, err))
	}
	return d
}
