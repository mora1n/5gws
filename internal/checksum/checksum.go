package checksum

import (
	"fmt"
	"os"
	"strings"
)

func ParseSHA256Text(text, asset string) (string, error) {
	line := firstNonEmptyLine(text)
	if line == "" {
		return "", fmt.Errorf("checksum is empty")
	}
	fields := strings.Fields(line)
	if len(fields) != 1 && len(fields) != 2 {
		return "", fmt.Errorf("checksum line must be '<sha256>' or '<sha256>  %s'", asset)
	}
	hash := fields[0]
	if len(hash) != 64 || !isHex(hash) {
		return "", fmt.Errorf("invalid sha256 hash %q", hash)
	}
	if len(fields) == 2 {
		name := strings.TrimPrefix(fields[1], "*")
		if name != asset {
			return "", fmt.Errorf("checksum filename %q does not match %q", name, asset)
		}
	}
	return strings.ToLower(hash), nil
}

func NormalizeBareSHA256File(path, asset string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := firstNonEmptyLine(string(data))
	if line == "" {
		return fmt.Errorf("%s is empty", path)
	}
	fields := strings.Fields(line)
	if len(fields) != 1 {
		return nil
	}
	hash := fields[0]
	if len(hash) != 64 || !isHex(hash) {
		return nil
	}
	return os.WriteFile(path, []byte(hash+"  "+asset+"\n"), 0o600)
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func isHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
