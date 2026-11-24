package engine

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Limits on untrusted input. Each can be raised or lowered with an environment
// variable; a value that is not a positive integer is ignored.
const (
	defaultMaxZipEntries    = 10_000
	defaultMaxZipEntryBytes = 256 << 20 // uncompressed, per member
	defaultMaxZipTotalBytes = 1 << 30   // uncompressed, whole archive
	zipRatioFloorBytes      = 1 << 20   // the ratio check applies to members above this size
	maxZipRatio             = 200       // uncompressed : compressed

	defaultMaxPixels        = 50_000_000
	defaultMaxMarkdownBytes = 64 << 20
)

// zipLimits bounds a zip-based document (docx, xlsx, pptx, epub, odt, ods, odp).
type zipLimits struct {
	MaxEntries    int
	MaxEntryBytes uint64
	MaxTotalBytes uint64
}

func envPositive(name string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(name), 10, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// zipLimitsFromEnv reads GLEANN_DOCS_ZIP_MAX_ENTRIES, _MAX_ENTRY_BYTES and
// _MAX_TOTAL_BYTES.
func zipLimitsFromEnv() zipLimits {
	return zipLimits{
		MaxEntries:    int(envPositive("GLEANN_DOCS_ZIP_MAX_ENTRIES", defaultMaxZipEntries)),
		MaxEntryBytes: uint64(envPositive("GLEANN_DOCS_ZIP_MAX_ENTRY_BYTES", defaultMaxZipEntryBytes)),
		MaxTotalBytes: uint64(envPositive("GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES", defaultMaxZipTotalBytes)),
	}
}

// MaxPixels is the largest image (width x height) that is decoded:
// GLEANN_DOCS_MAX_PIXELS, default 50 million.
func MaxPixels() int64 { return envPositive("GLEANN_DOCS_MAX_PIXELS", defaultMaxPixels) }

// MaxMarkdownBytes is the largest Markdown result that is returned:
// GLEANN_DOCS_MAX_MARKDOWN_BYTES, default 64 MiB.
func MaxMarkdownBytes() int64 {
	return envPositive("GLEANN_DOCS_MAX_MARKDOWN_BYTES", defaultMaxMarkdownBytes)
}

// zipExts are the document formats that are zip archives.
var zipExts = map[string]bool{
	".docx": true, ".xlsx": true, ".pptx": true, ".epub": true,
	".odt": true, ".ods": true, ".odp": true,
}

// zipPreflight reads the central directory of path and refuses an archive with
// too many entries, a member or total uncompressed size over the limits, or an
// implausible compression ratio. Nothing is decompressed. A file that is not a
// valid zip is ErrCorruptInput.
func zipPreflight(path string, lim zipLimits) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrCorruptInput, filepath.Base(path), err)
	}
	defer r.Close()
	if len(r.File) > lim.MaxEntries {
		return fmt.Errorf("%w: %s has %d entries (limit %d)", ErrTooLarge, filepath.Base(path), len(r.File), lim.MaxEntries)
	}
	var total uint64
	for _, f := range r.File {
		if f.UncompressedSize64 > lim.MaxEntryBytes {
			return fmt.Errorf("%w: %s member %q is %d bytes uncompressed (limit %d)",
				ErrTooLarge, filepath.Base(path), f.Name, f.UncompressedSize64, lim.MaxEntryBytes)
		}
		if f.UncompressedSize64 > zipRatioFloorBytes && f.CompressedSize64 > 0 &&
			f.UncompressedSize64/f.CompressedSize64 > maxZipRatio {
			return fmt.Errorf("%w: %s member %q expands more than %d:1", ErrTooLarge, filepath.Base(path), f.Name, maxZipRatio)
		}
		total += f.UncompressedSize64
		if total > lim.MaxTotalBytes {
			return fmt.Errorf("%w: %s is more than %d bytes uncompressed", ErrTooLarge, filepath.Base(path), lim.MaxTotalBytes)
		}
	}
	return nil
}

// boundedEntry reads a zip member and fails with ErrTooLarge once more bytes
// arrive than the header declared (a lying header).
type boundedEntry struct {
	rc   io.ReadCloser
	left int64
	name string
}

func (b *boundedEntry) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, fmt.Errorf("%w: zip member %q is larger than its header says", ErrTooLarge, b.name)
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - 1, fmt.Errorf("%w: zip member %q is larger than its header says", ErrTooLarge, b.name)
	}
	return n, err
}

func (b *boundedEntry) Close() error { return b.rc.Close() }

// zipBudget is the number of uncompressed bytes one conversion may read from
// its archive in total. Every openEntry charges it as the bytes are read, so a
// member that is referenced (and therefore read) many times is paid for each
// time. A nil budget is unlimited (tests).
type zipBudget struct {
	left int64
}

// newZipBudget returns the per-conversion budget, derived from the total cap
// that zipPreflight enforces on the archive.
func newZipBudget() *zipBudget {
	n := zipLimitsFromEnv().MaxTotalBytes
	if n > 1<<62 {
		n = 1 << 62
	}
	return &zipBudget{left: int64(n)}
}

func (b *zipBudget) charge(n int) error {
	if b == nil {
		return nil
	}
	b.left -= int64(n)
	if b.left < 0 {
		return fmt.Errorf("%w: the conversion reads more than the archive total limit of zip data (GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES)", ErrTooLarge)
	}
	return nil
}

// budgetedEntry charges a zipBudget for every byte it returns.
type budgetedEntry struct {
	io.ReadCloser
	budget *zipBudget
}

func (b *budgetedEntry) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if cerr := b.budget.charge(n); cerr != nil {
			return 0, cerr
		}
	}
	return n, err
}

// openEntry opens a zip member for reading, capped at its declared size and
// charged against the conversion's budget.
func openEntry(budget *zipBudget, f *zip.File) (io.ReadCloser, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	n := int64(f.UncompressedSize64)
	if n < 0 { // beyond int64: the preflight refuses these first
		n = 1<<63 - 2
	}
	return &budgetedEntry{ReadCloser: &boundedEntry{rc: rc, left: n, name: f.Name}, budget: budget}, nil
}

// outBudget is the output budget of ONE conversion: the Markdown cap, shared by
// every buffer that holds output text (document, slide, cell, list, sheet and
// page buffers). take charges it, give refunds a buffer that was merged into
// its parent; once a charge fails the error is sticky. It travels with the
// conversion through the context (budgetFrom).
// maxListLevel is the deepest list indent that is rendered. Word and
// PowerPoint define nine levels (0..8); a larger or negative value from the
// document is clamped, never used as a repeat count.
const maxListLevel = 8

func clampListLevel(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxListLevel {
		return maxListLevel
	}
	return n
}

type outBudget struct {
	left  int64
	limit int64
	err   error
}

func newOutBudget() *outBudget {
	n := MaxMarkdownBytes()
	return &outBudget{left: n, limit: n}
}

// take charges n bytes; past the budget it fails with ErrTooLarge (sticky).
func (b *outBudget) take(n int) error {
	if b.err != nil {
		return b.err
	}
	if int64(n) > b.left {
		b.err = fmt.Errorf("%w: converted text exceeds %d bytes (GLEANN_DOCS_MAX_MARKDOWN_BYTES)", ErrTooLarge, b.limit)
		return b.err
	}
	b.left -= int64(n)
	return nil
}

// give refunds n bytes that are no longer held.
func (b *outBudget) give(n int) { b.left += int64(n) }

type budgetKey struct{}

// budgetFrom returns the conversion's output budget, creating it (and the
// context that carries it) on first use. A nested Convert shares its parent's.
func budgetFrom(ctx context.Context) (context.Context, *outBudget) {
	if b, ok := ctx.Value(budgetKey{}).(*outBudget); ok {
		return ctx, b
	}
	b := newOutBudget()
	return context.WithValue(ctx, budgetKey{}, b), b
}

// mdBuilder is the only builder for output text. Every byte written is charged
// to the conversion's outBudget; a write past it fails with ErrTooLarge and
// stores nothing. Extractors check Err in their loops and stop.
type mdBuilder struct {
	sb   strings.Builder
	ob   *outBudget
	held int // bytes this builder has charged
}

// newMDBuilder is a builder with a budget of its own (library use, tests).
func newMDBuilder() *mdBuilder { return &mdBuilder{ob: newOutBudget()} }

// newMDBuilderOn is a builder that draws on the conversion's budget.
func newMDBuilderOn(ob *outBudget) *mdBuilder { return &mdBuilder{ob: ob} }

func (m *mdBuilder) Write(p []byte) (int, error) {
	if err := m.ob.take(len(p)); err != nil {
		return 0, err
	}
	m.held += len(p)
	return m.sb.Write(p)
}

func (m *mdBuilder) WriteString(s string) (int, error) {
	if err := m.ob.take(len(s)); err != nil {
		return 0, err
	}
	m.held += len(s)
	return m.sb.WriteString(s)
}

func (m *mdBuilder) Len() int       { return m.sb.Len() }
func (m *mdBuilder) String() string { return m.sb.String() }

// Err is the budget's sticky ErrTooLarge once it was exceeded, nil before.
func (m *mdBuilder) Err() error { return m.ob.err }

// Discard drops the content and refunds what it charged: a fragment that was
// copied into its parent (which charged it again) is released this way.
func (m *mdBuilder) Discard() {
	m.ob.give(m.held)
	m.held = 0
	m.sb.Reset()
}

// joinBudgeted joins parts that were charged to ob when they were collected
// (chargeText) into one string, charged once. The refund and the new charge
// cancel out, so a join never fails for text that was already accepted.
func joinBudgeted(ob *outBudget, parts []string, sep string) (string, error) {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	ob.give(total)
	out := newMDBuilderOn(ob)
	for i, p := range parts {
		if i > 0 {
			if _, err := out.WriteString(sep); err != nil {
				return "", err
			}
		}
		if _, err := out.WriteString(p); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

// chargeText charges text that is kept as a plain string (a page, a chapter, a
// converter's output) to the conversion's budget.
func chargeText(ob *outBudget, s string) error { return ob.take(len(s)) }

// checkImageConfig reads only the header of an image and refuses one over the
// pixel budget, so a few kilobytes cannot make the decoder allocate gigabytes.
// r is rewound to the start.
func checkImageConfig(r io.ReadSeeker, maxPixels int64) (err error) {
	defer recoverImagePanic(&err)
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptInput, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("%w: image declares %dx%d pixels", ErrCorruptInput, cfg.Width, cfg.Height)
	}
	// Divide instead of multiplying: 4294967295x4294967295 overflows int64.
	if maxPixels <= 0 || int64(cfg.Width) > maxPixels/int64(cfg.Height) {
		return fmt.Errorf("%w: %dx%d pixels (limit %d)", ErrImageTooLarge, cfg.Width, cfg.Height, maxPixels)
	}
	_, err = r.Seek(0, io.SeekStart)
	return err
}

// recoverImagePanic turns a panic of an image decoder on a hostile file into
// ErrCorruptInput, so one bad image cannot take the connection down.
func recoverImagePanic(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%w: image decoder failed: %v", ErrCorruptInput, r)
	}
}

// decodeImageSafe is image.Decode with decoder panics reported as ErrCorruptInput.
func decodeImageSafe(r io.Reader) (img image.Image, err error) {
	defer recoverImagePanic(&err)
	img, _, err = image.Decode(r)
	return img, err
}

// checkImageFile applies checkImageConfig to a file.
func checkImageFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return checkImageConfig(f, MaxPixels())
}

// preflight runs the cheap size checks that must happen before any extractor
// (or the anydoc fast path) touches the file.
func preflight(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	var err error
	switch {
	case zipExts[ext]:
		err = zipPreflight(path, zipLimitsFromEnv())
	case ext == ".png" || ext == ".jpg" || ext == ".jpeg":
		err = checkImageFile(path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil // the converter reports a missing file in its own words
	}
	return err
}
