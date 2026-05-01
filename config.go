package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	SessionKey string `json:"session_key"`
	OrgID      string `json:"org_id"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "claude-usage-widget", "config.json"), nil
}

func LoadConfig() (*Config, string, error) {
	p, err := configPath()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, p, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, p, err
	}
	if c.SessionKey == "" || c.OrgID == "" {
		return nil, p, errors.New("session_key or org_id missing")
	}
	return &c, p, nil
}

func SaveConfig(c *Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
