package inventory

import (
	"fmt"
	"os"
	"path/filepath"
)

func loadNames(root string) (map[string]string, string, error) {
	path := filepath.Join(root, NamesFilename)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("inventory: reading %s: %w", path, err)
	}
	var doc struct {
		Names map[string]string `yaml:"names"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, fmt.Sprintf("%s: %s", NamesFilename, err), nil
	}
	return doc.Names, "", nil
}
