package annotation

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The helpers in this file read the pre-SQLite folder layout:
//
//	classes.txt
//	images/<name>
//	labels/<name>.txt
//
// They are only used to import old workspaces; SQLite is the source of truth
// after migration.

func readClassesFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	names := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names, nil
}

func readLabelsFile(path string) ([]Label, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Label{}, nil
		}
		return nil, err
	}
	defer f.Close()

	labels := []Label{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		l, err := parseLabelLine(text)
		if err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %v", ErrInvalid, filepath.Base(path), line, err)
		}
		labels = append(labels, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return labels, nil
}

func listImageFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !isImage(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

func labelFileName(imageName string) string {
	stem := strings.TrimSuffix(imageName, filepath.Ext(imageName))
	return stem + ".txt"
}
