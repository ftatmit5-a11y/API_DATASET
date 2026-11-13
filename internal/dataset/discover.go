package dataset

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

var imageExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".bmp":  {},
	".webp": {},
	".tif":  {},
	".tiff": {},
	".gif":  {},
}

func discover(root string) (paired []Sample, unlabeled int, err error) {
	var images []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if name == "__MACOSX" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || name == ".DS_Store" {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := imageExts[ext]; ok {
			images = append(images, path)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	for _, imagePath := range images {
		label := findLabel(root, imagePath)
		if label == "" {
			unlabeled++
			continue
		}
		paired = append(paired, Sample{
			ImagePath: imagePath,
			LabelPath: label,
			Rel:       relImageName(root, imagePath),
		})
	}
	return paired, unlabeled, nil
}

func findLabel(root, imagePath string) string {
	stem := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	candidates := []string{
		filepath.Join(filepath.Dir(imagePath), stem+".txt"),
	}
	rel, err := filepath.Rel(root, imagePath)
	if err == nil {
		parts := strings.Split(rel, string(os.PathSeparator))
		for i := 0; i < len(parts)-1; i++ {
			if strings.EqualFold(parts[i], "images") {
				cp := append([]string(nil), parts...)
				cp[i] = "labels"
				cp[len(cp)-1] = stem + ".txt"
				candidates = append(candidates, filepath.Join(append([]string{root}, cp...)...))
			}
		}
	}
	for _, c := range candidates {
		info, statErr := os.Stat(c)
		if statErr == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func relImageName(root, imagePath string) string {
	rel, err := filepath.Rel(root, imagePath)
	if err != nil {
		return filepath.Base(imagePath)
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	var keep []string
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "train", "val", "valid", "validation", "test", "images", "labels":
			continue
		default:
			keep = append(keep, p)
		}
	}
	keep = append(keep, parts[len(parts)-1])
	return filepath.Join(keep...)
}

func findClassNames(root string) ([]string, error) {
	type hit struct {
		path     string
		priority int
		depth    int
	}
	var hits []hit
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "__MACOSX" {
				return filepath.SkipDir
			}
			return nil
		}
		base := strings.ToLower(d.Name())
		priority := -1
		switch base {
		case "classes.txt", "classes.names":
			priority = 0
		case "obj.names":
			priority = 1
		case "data.yaml", "data.yml", "dataset.yaml", "dataset.yml":
			priority = 2
		}
		if priority < 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		hits = append(hits, hit{path: path, priority: priority, depth: depth})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	best := hits[0]
	for _, h := range hits[1:] {
		if h.priority < best.priority || (h.priority == best.priority && h.depth < best.depth) {
			best = h
		}
	}
	data, err := os.ReadFile(best.path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(best.path))
	if ext == ".yaml" || ext == ".yml" {
		names, ok := parseNamesYAML(string(data))
		if !ok {
			return nil, fmt.Errorf("could not read class names from %s", filepath.Base(best.path))
		}
		return names, nil
	}
	return parseNameList(string(data)), nil
}

func parseNameList(text string) []string {
	var names []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names
}

// parseNamesYAML reads a small subset of YOLO data.yaml name lists:
//
//	names: [cat, dog]
//	names:
//	  - cat
//	  - dog
//	names:
//	  0: cat
//	  1: dog
func parseNamesYAML(text string) ([]string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if !strings.HasPrefix(trim, "names:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trim, "names:"))
		if rest != "" {
			if strings.HasPrefix(rest, "[") {
				return parseInlineList(rest), true
			}
			return []string{unquote(rest)}, true
		}
		return parseNamesBlock(lines[i+1:], indentOf(line))
	}
	return nil, false
}

func parseInlineList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := splitCSV(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = unquote(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitCSV(s string) []string {
	var parts []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			b.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		case c == ',':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	parts = append(parts, b.String())
	return parts
}

func parseNamesBlock(lines []string, parentIndent int) ([]string, bool) {
	type kv struct {
		idx  int
		name string
		ok   bool
	}
	var indexed []kv
	var listed []string
	mode := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		ind := indentOf(line)
		if ind <= parentIndent {
			break
		}
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "- ") || trim == "-" {
			if mode == "" {
				mode = "list"
			}
			if mode != "list" {
				return nil, false
			}
			listed = append(listed, unquote(strings.TrimSpace(strings.TrimPrefix(trim, "-"))))
			continue
		}
		key, val, ok := strings.Cut(trim, ":")
		if !ok {
			return nil, false
		}
		if mode == "" {
			mode = "map"
		}
		if mode != "map" {
			return nil, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return nil, false
		}
		indexed = append(indexed, kv{idx: n, name: unquote(strings.TrimSpace(val)), ok: true})
	}
	if mode == "list" {
		return listed, true
	}
	if mode != "map" || len(indexed) == 0 {
		return nil, false
	}
	max := 0
	for _, item := range indexed {
		if item.idx > max {
			max = item.idx
		}
	}
	names := make([]string, max+1)
	for i := range names {
		names[i] = fmt.Sprintf("class_%d", i)
	}
	for _, item := range indexed {
		if item.idx >= 0 {
			names[item.idx] = item.name
		}
	}
	return names, true
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' {
			n++
			continue
		}
		if r == '\t' {
			n += 2
			continue
		}
		break
	}
	return n
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			u, err := strconv.Unquote(s)
			if err == nil {
				return u
			}
			return s[1 : len(s)-1]
		}
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func normalizeNames(names []string, samples []Sample) []string {
	maxID := maxClassID(samples)
	if names == nil {
		names = []string{}
	}
	cleaned := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		cleaned = append(cleaned, n)
	}
	need := maxID + 1
	if need < 1 {
		need = len(cleaned)
	}
	if need < len(cleaned) {
		need = len(cleaned)
	}
	if need == 0 {
		return []string{}
	}
	out := make([]string, need)
	for i := 0; i < need; i++ {
		if i < len(cleaned) {
			out[i] = cleaned[i]
			continue
		}
		out[i] = fmt.Sprintf("class_%d", i)
	}
	return out
}

func maxClassID(samples []Sample) int {
	max := -1
	for _, s := range samples {
		f, err := os.Open(s.LabelPath)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			field := line
			if i := strings.IndexFunc(line, unicode.IsSpace); i >= 0 {
				field = line[:i]
			}
			id, err := strconv.Atoi(field)
			if err == nil && id > max {
				max = id
			}
		}
		f.Close()
	}
	return max
}

func shuffleOrder(n int, seed int64) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order
}
