package platforms

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// updateEnvValue updates or appends a key=value pair in the .env file
// and sets it in the current process environment.
func updateEnvValue(key, value string) error {
	envPath := ".env"

	content, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .env: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)
	newLine := key + "=" + value

	found := false
	for i, line := range lines {
		if re.MatchString(line) {
			lines[i] = newLine
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, newLine)
	}

	output := strings.Join(lines, "\n")
	// Ensure single trailing newline
	output = strings.TrimRight(output, "\n") + "\n"

	if err := os.WriteFile(envPath, []byte(output), 0600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}

	return os.Setenv(key, value)
}
