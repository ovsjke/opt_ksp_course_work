package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	Port, DatabaseURL string
	SessionTTL        time.Duration
}

func Load() (Config, error) {
	c := Config{Port: os.Getenv("APP_PORT"), DatabaseURL: os.Getenv("DATABASE_URL"), SessionTTL: 24 * time.Hour}
	if c.Port == "" {
		c.Port = "8080"
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if v := os.Getenv("SESSION_TTL"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil || d < time.Second {
			return c, fmt.Errorf("invalid SESSION_TTL")
		}
		c.SessionTTL = d
	}
	return c, nil
}
