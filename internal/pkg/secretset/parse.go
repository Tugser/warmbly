package secretset

import (
	"encoding/json"
	"errors"
	"strings"
)

// Parse reads a bounded JSON array without including secret values in errors.
func Parse(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(value), &keys); err != nil || keys == nil || len(keys) > 4 {
		return nil, errors.New("expected a JSON array containing at most four nonempty secrets")
	}
	for _, key := range keys {
		if key == "" {
			return nil, errors.New("expected a JSON array containing at most four nonempty secrets")
		}
	}
	return keys, nil
}
