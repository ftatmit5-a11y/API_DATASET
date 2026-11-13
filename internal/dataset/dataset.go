// Package dataset reads a YOLO folder (images + labels), splits it into
// train/val/test, and writes an Ultralytics-style data.yaml.
package dataset

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxZipFiles     = 100_000
	maxUncompressed = 2 << 30 // 2 GiB
	maxSingleFile   = 512 << 20
)

// Options controls how a dataset is split.
type Options struct {
	TrainRatio float64
	ValRatio   float64
	TestRatio  float64
	// Names overrides class names found inside the archive when non-empty.
	Names []string
	Seed  int64
}

// Summary is the result of a successful split.
type Summary struct {
	Total            int      `json:"total"`
	Train            int      `json:"train"`
	Val              int      `json:"val"`
	Test             int      `json:"test"`
	UnlabeledSkipped int      `json:"unlabeled_skipped"`
	Classes          []string `json:"classes"`
	NC               int      `json:"nc"`
	TrainRatio       float64  `json:"train_ratio"`
	ValRatio         float64  `json:"val_ratio"`
	TestRatio        float64  `json:"test_ratio"`
	Seed             int64    `json:"seed"`
}

// Sample is one image and its YOLO label file.
type Sample struct {
	ImagePath string
	LabelPath string
	Rel       string // path under images/ and labels/, using OS separators
}

// DefaultRatios is 70% train, 20% val, 10% test.
func DefaultRatios() (train, val, test float64) {
	return 0.7, 0.2, 0.1
}

// ValidateRatios checks that each ratio is non-negative and that they sum to 1.
func ValidateRatios(train, val, test float64) error {
	if train < 0 || val < 0 || test < 0 || math.IsNaN(train) || math.IsNaN(val) || math.IsNaN(test) {
		return fmt.Errorf("ratios must be zero or positive")
	}
	if train == 0 {
		return fmt.Errorf("train_ratio must be greater than 0")
	}
	sum := train + val + test
	if math.Abs(sum-1) > 0.001 {
		return fmt.Errorf("train_ratio + val_ratio + test_ratio must equal 1 (got %.4f)", sum)
	}
	return nil
}

// Build reads srcDir and writes a YOLO layout into outDir:
//
//	data.yaml
//	train/images, train/labels
//	val/images, val/labels
//	test/images, test/labels
func Build(srcDir, outDir string, opt Options) (Summary, error) {
	if err := ValidateRatios(opt.TrainRatio, opt.ValRatio, opt.TestRatio); err != nil {
		return Summary{}, err
	}
	samples, skipped, err := discover(srcDir)
	if err != nil {
		return Summary{}, err
	}
	if len(samples) == 0 {
		return Summary{}, fmt.Errorf("no image/label pairs found (skipped %d images without labels)", skipped)
	}

	names := opt.Names
	if len(names) == 0 {
		names, err = findClassNames(srcDir)
		if err != nil {
			return Summary{}, err
		}
	}
	names = normalizeNames(names, samples)

	order := shuffleOrder(len(samples), opt.Seed)
	nTrain, nVal, nTest := splitCounts(len(samples), opt.TrainRatio, opt.ValRatio, opt.TestRatio)

	groups := map[string][]Sample{
		"train": pick(samples, order[:nTrain]),
		"val":   pick(samples, order[nTrain:nTrain+nVal]),
		"test":  pick(samples, order[nTrain+nVal:]),
	}
	for split, items := range groups {
		if err := place(outDir, split, items); err != nil {
			return Summary{}, err
		}
	}
	if err := writeDataYAML(filepath.Join(outDir, "data.yaml"), names, nVal > 0, nTest > 0); err != nil {
		return Summary{}, err
	}

	return Summary{
		Total:            len(samples),
		Train:            nTrain,
		Val:              nVal,
		Test:             nTest,
		UnlabeledSkipped: skipped,
		Classes:          names,
		NC:               len(names),
		TrainRatio:       opt.TrainRatio,
		ValRatio:         opt.ValRatio,
		TestRatio:        opt.TestRatio,
		Seed:             opt.Seed,
	}, nil
}

// BuildZip extracts a YOLO zip, splits it, and writes the result as a zip file.
func BuildZip(zipPath, outZip string, opt Options) (Summary, error) {
	src, err := os.MkdirTemp("", "yolo-src-*")
	if err != nil {
		return Summary{}, err
	}
	defer os.RemoveAll(src)

	if err := unzip(zipPath, src); err != nil {
		return Summary{}, err
	}
	out, err := os.MkdirTemp("", "yolo-out-*")
	if err != nil {
		return Summary{}, err
	}
	defer os.RemoveAll(out)

	summary, err := Build(src, out, opt)
	if err != nil {
		return Summary{}, err
	}
	if err := zipDir(out, outZip); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

func pick(samples []Sample, idxs []int) []Sample {
	out := make([]Sample, len(idxs))
	for i, idx := range idxs {
		out[i] = samples[idx]
	}
	return out
}

func place(outDir, split string, items []Sample) error {
	used := map[string]struct{}{}
	for _, item := range items {
		rel := uniqueRel(item.Rel, used)
		imgDest := filepath.Join(outDir, split, "images", rel)
		if err := copyFile(item.ImagePath, imgDest); err != nil {
			return err
		}
		labelRel := strings.TrimSuffix(rel, filepath.Ext(rel)) + ".txt"
		labelDest := filepath.Join(outDir, split, "labels", labelRel)
		if err := copyFile(item.LabelPath, labelDest); err != nil {
			return err
		}
	}
	return nil
}

func uniqueRel(rel string, used map[string]struct{}) string {
	key := strings.ToLower(filepath.ToSlash(rel))
	if _, ok := used[key]; !ok {
		used[key] = struct{}{}
		return rel
	}
	ext := filepath.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s_%d%s", stem, i, ext)
		ckey := strings.ToLower(filepath.ToSlash(cand))
		if _, ok := used[ckey]; !ok {
			used[ckey] = struct{}{}
			return cand
		}
	}
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// splitCounts uses the largest-remainder method so the three buckets sum to n.
func splitCounts(n int, train, val, test float64) (int, int, int) {
	if n == 0 {
		return 0, 0, 0
	}
	raws := []float64{float64(n) * train, float64(n) * val, float64(n) * test}
	counts := make([]int, 3)
	fracs := make([]float64, 3)
	sum := 0
	for i, raw := range raws {
		counts[i] = int(math.Floor(raw + 1e-9))
		fracs[i] = raw - float64(counts[i])
		sum += counts[i]
	}
	left := n - sum
	order := []int{0, 1, 2}
	sort.SliceStable(order, func(i, j int) bool {
		return fracs[order[i]] > fracs[order[j]]
	})
	for k := 0; k < left; k++ {
		counts[order[k%3]]++
	}
	if counts[0] == 0 {
		switch {
		case counts[1] >= counts[2] && counts[1] > 0:
			counts[1]--
			counts[0]++
		case counts[2] > 0:
			counts[2]--
			counts[0]++
		}
	}
	return counts[0], counts[1], counts[2]
}
