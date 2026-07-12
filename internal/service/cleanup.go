package service

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

func PruneRevisionDirs(stateDir string, keep ...int64) error {
	kept := make(map[int64]bool, len(keep))
	for _, id := range keep {
		kept[id] = true
	}
	root := filepath.Join(stateDir, "revisions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || kept[id] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func PrunePreflightDirs(stateDir string) error {
	matches, err := filepath.Glob(filepath.Join(stateDir, "preflight-*"))
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			return err
		}
	}
	return nil
}
