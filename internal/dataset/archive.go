package dataset

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("invalid zip: %w", err)
	}
	defer r.Close()

	wrapper := commonRootDir(r.File)
	var files int
	var total int64
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "__MACOSX/") || strings.Contains(f.Name, "/__MACOSX/") {
			continue
		}
		name := f.Name
		if wrapper != "" {
			name = stripRootDir(name, wrapper)
			if name == "" {
				continue
			}
		}
		clean, err := safeZipPath(name)
		if err != nil {
			return err
		}
		if clean == "" || clean == "." {
			continue
		}
		target := filepath.Join(dest, clean)
		if !isInside(dest, target) {
			return fmt.Errorf("unsafe zip path %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		files++
		if files > maxZipFiles {
			return fmt.Errorf("zip has too many files (limit %d)", maxZipFiles)
		}
		if f.UncompressedSize64 > maxSingleFile {
			return fmt.Errorf("file %s exceeds size limit", f.Name)
		}
		n, err := extractFile(f, target)
		if err != nil {
			return err
		}
		total += n
		if total > maxUncompressed {
			return fmt.Errorf("uncompressed zip exceeds size limit")
		}
	}
	return nil
}

// commonRootDir returns the name of a single top-level folder that wraps every
// entry in the archive (for example "MyDataset" in MyDataset/images/a.jpg).
// It returns "" when the archive is not wrapped in exactly one folder.
func commonRootDir(files []*zip.File) string {
	root := ""
	nested := false
	for _, f := range files {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") {
			continue
		}
		for strings.HasPrefix(name, "./") {
			name = name[2:]
		}
		if name == "" || name == "." {
			continue
		}
		isDir := strings.HasSuffix(name, "/") || f.FileInfo().IsDir()
		name = strings.TrimSuffix(name, "/")
		parts := strings.SplitN(name, "/", 2)
		first := parts[0]
		if first == "" || first == "." || first == ".." {
			return ""
		}
		if root == "" {
			root = first
		} else if !strings.EqualFold(root, first) {
			return ""
		}
		if len(parts) == 1 {
			if isDir {
				continue
			}
			// A file at the archive root means there is no wrapper folder.
			return ""
		}
		nested = true
	}
	if !nested || root == "" || isStructuralDir(root) {
		return ""
	}
	return root
}

func isStructuralDir(name string) bool {
	switch strings.ToLower(name) {
	case "images", "labels", "train", "val", "valid", "validation", "test":
		return true
	}
	return false
}

// stripRootDir removes the wrapper folder from a zip entry name.
func stripRootDir(name, wrapper string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	parts := strings.SplitN(name, "/", 2)
	if !strings.EqualFold(parts[0], wrapper) {
		return name
	}
	if len(parts) == 1 {
		return ""
	}
	return parts[1]
}

func extractFile(f *zip.File, target string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	in, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(in, maxSingleFile+1))
	if err != nil {
		return n, err
	}
	if n > maxSingleFile {
		return n, fmt.Errorf("file %s exceeds size limit", f.Name)
	}
	return n, out.Close()
}

func safeZipPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe zip path %q", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("unsafe zip path %q", name)
	}
	clean := pathCleanSlash(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("unsafe zip path %q", name)
	}
	return filepath.FromSlash(clean), nil
}

func pathCleanSlash(name string) string {
	parts := strings.Split(name, "/")
	var stack []string
	for _, p := range parts {
		switch p {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "../" + name
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, p)
		}
	}
	return strings.Join(stack, "/")
}

func isInside(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func zipDir(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	err = filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}
