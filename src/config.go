package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	CatalogPath   string
	DataDirectory string
	Debug         bool
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	config := &Config{
		CatalogPath:   "./data/catalog.json",
		DataDirectory: "./data",
		Debug:         false,
	}

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Abaikan baris kosong dan komentar.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("invalid config line: %q", line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "catalog_path":
			config.CatalogPath = value

		case "data_directory":
			config.DataDirectory = value

		case "debug":
			boolean, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf(
					"invalid debug value %q: %w",
					value,
					err,
				)
			}
			config.Debug = boolean

		default:
			return nil, fmt.Errorf("unknown config key: %q", key)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return config, nil
}
