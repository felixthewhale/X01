package core

import (
	"bufio"
	"os"
	"strings"
)

// LoadEnv reads a .env file and sets the environment variables.
func LoadEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		
		// Remove quotes if present
		val = strings.Trim(val, `"'`)
		
		os.Setenv(key, val)
	}

	return scanner.Err()
}
