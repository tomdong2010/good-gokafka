// Package config loads settings shared by the producer and consumer from the
// environment, optionally seeded from a local .env file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// LoadDotEnv loads .env from the working directory if present. Variables already
// set in the process environment take precedence over the file.
func LoadDotEnv() error {
	err := godotenv.Load()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}

// Brokers returns the Kafka bootstrap servers from KAFKA_BROKERS (comma separated).
// The first set variable among legacyKeys is used as a fallback so older .env
// files keep working.
func Brokers(legacyKeys ...string) ([]string, error) {
	for _, key := range append([]string{"KAFKA_BROKERS"}, legacyKeys...) {
		if v, ok := os.LookupEnv(key); ok {
			if brokers := splitList(v); len(brokers) > 0 {
				return brokers, nil
			}
		}
	}
	return nil, errors.New("KAFKA_BROKERS is not set")
}

// Required returns the value of key or an error if it is unset or blank.
func Required(key string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return "", fmt.Errorf("%s is not set", key)
	}
	return v, nil
}

// String returns the value of key, or def if it is unset or blank.
func String(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Duration parses key as a time.Duration, returning def if it is unset.
func Duration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// Int parses key as an integer, returning def if it is unset.
func Int(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// Bool parses key as a boolean (1/0, true/false, ...), returning def if it is unset.
func Bool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

// List returns key split on commas with blanks dropped.
func List(key string) []string {
	return splitList(os.Getenv(key))
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Logger builds the process logger from LOG_LEVEL (debug, info, warn, error)
// and LOG_FORMAT (text or json).
func Logger() (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(String("LOG_LEVEL", "info"))); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	opts := &slog.HandlerOptions{Level: level}
	switch format := strings.ToLower(String("LOG_FORMAT", "text")); format {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, opts)), nil
	default:
		return nil, fmt.Errorf("LOG_FORMAT: unknown format %q (want text or json)", format)
	}
}
