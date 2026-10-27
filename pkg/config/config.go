// Package config содержит вспомогательные функции чтения конфигурации из
// переменных окружения (принцип 12-factor app: конфигурация отделена от кода).
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// String возвращает значение переменной окружения или значение по умолчанию.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// Int читает целое число.
func Int(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// Int64 читает целое 64-битное число.
func Int64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

// Float читает число с плавающей точкой.
func Float(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n
		}
	}
	return def
}

// Bool читает логическое значение (true/false/1/0/yes/no).
func Bool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

// Duration читает длительность в формате Go (например, 500ms, 2s, 24h).
func Duration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

// List читает список значений, разделённых запятыми.
func List(key string, def []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
