package engine

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func buildZip(t *testing.T, name string, build func(zw *zip.Writer)) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	build(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func headerOnlyPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		c := crc32.NewIEEE()
		c.Write([]byte(kind))
		c.Write(data)
		_ = binary.Write(&b, binary.BigEndian, c.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write([]byte{0, 0, 0, 0, 0})
	_ = zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return b.Bytes()
}

func TestZipPreflightLimits(t *testing.T) {
	lim := zipLimits{MaxEntries: 5, MaxEntryBytes: 1000, MaxTotalBytes: 1500}
	small := buildZip(t, "ok.docx", func(zw *zip.Writer) {
		w, _ := zw.Create("a.xml")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 500))
	})
	if err := zipPreflight(small, lim); err != nil {
		t.Fatalf("small archive refused: %v", err)
	}
	many := buildZip(t, "many.docx", func(zw *zip.Writer) {
		for i := 0; i < 6; i++ {
			_, _ = zw.Create(fmt.Sprintf("%d", i))
		}
	})
	if err := zipPreflight(many, lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("6 entries over a limit of 5: %v", err)
	}
	bigMember := buildZip(t, "big.docx", func(zw *zip.Writer) {
		w, _ := zw.Create("a.xml")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1001))
	})
	if err := zipPreflight(bigMember, lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("member over the limit: %v", err)
	}
	total := buildZip(t, "total.docx", func(zw *zip.Writer) {
		for i := 0; i < 3; i++ {
			w, _ := zw.Create(fmt.Sprintf("%d", i))
			_, _ = w.Write(bytes.Repeat([]byte("a"), 600))
		}
	})
	if err := zipPreflight(total, lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("total over the limit: %v", err)
	}
	notZip := filepath.Join(t.TempDir(), "x.docx")
	_ = os.WriteFile(notZip, []byte("plain"), 0o644)
	if err := zipPreflight(notZip, lim); !errors.Is(err, ErrCorruptInput) {
		t.Errorf("not a zip: %v", err)
	}
}

func TestZipPreflightRatio(t *testing.T) {
	zeros := make([]byte, 30<<20)
	var comp bytes.Buffer
	fw, _ := flate.NewWriter(&comp, flate.BestCompression)
	_, _ = fw.Write(zeros)
	_ = fw.Close()
	p := buildZip(t, "ratio.docx", func(zw *zip.Writer) {
		w, _ := zw.CreateRaw(&zip.FileHeader{Name: "a.xml", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(zeros),
			CompressedSize64: uint64(comp.Len()), UncompressedSize64: uint64(len(zeros))})
		_, _ = w.Write(comp.Bytes())
	})
	// Well inside the byte limits: only the ratio check can refuse it.
	err := zipPreflight(p, zipLimits{MaxEntries: 10, MaxEntryBytes: 256 << 20, MaxTotalBytes: 1 << 30})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("30 MiB of zeros in %d bytes: %v", comp.Len(), err)
	}
}

// A member whose header understates its size is cut off with ErrTooLarge.
func TestOpenEntryStopsLyingHeader(t *testing.T) {
	real := bytes.Repeat([]byte("z"), 200000)
	var comp bytes.Buffer
	fw, _ := flate.NewWriter(&comp, flate.BestSpeed)
	_, _ = fw.Write(real)
	_ = fw.Close()
	p := buildZip(t, "lie.docx", func(zw *zip.Writer) {
		w, _ := zw.CreateRaw(&zip.FileHeader{Name: "a.xml", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(real),
			CompressedSize64: uint64(comp.Len()), UncompressedSize64: 1000})
		_, _ = w.Write(comp.Bytes())
	})
	r, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rc, err := openEntry(nil, r.File[0])
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	// Current archive/zip already fails such a member with zip.ErrFormat; the
	// bound is the second line of defence. Either way the read must stop.
	if !errors.Is(err, ErrTooLarge) && !errors.Is(err, zip.ErrFormat) {
		t.Fatalf("read %d bytes, err %v; want a refusal", n, err)
	}
	if n > 1000 {
		t.Errorf("returned %d bytes of a member declared as 1000", n)
	}
}

func TestOpenEntryReadsHonestMember(t *testing.T) {
	data := bytes.Repeat([]byte("ab"), 5000)
	p := buildZip(t, "ok.docx", func(zw *zip.Writer) {
		w, _ := zw.Create("a.xml")
		_, _ = w.Write(data)
	})
	r, _ := zip.OpenReader(p)
	defer r.Close()
	rc, err := openEntry(nil, r.File[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read %d bytes, err %v", len(got), err)
	}
}

func TestCheckImageConfigRefusesBeforeDecode(t *testing.T) {
	// 40000x40000 declared, a few bytes of data: a full decode would allocate 6.4 GB.
	r := bytes.NewReader(headerOnlyPNG(40000, 40000))
	start := time.Now()
	err := checkImageConfig(r, 50_000_000)
	if !errors.Is(err, ErrImageTooLarge) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("the header check was slow")
	}
	ok := bytes.NewReader(headerOnlyPNG(100, 100))
	if err := checkImageConfig(ok, 50_000_000); err != nil {
		t.Fatalf("100x100: %v", err)
	}
	if pos, _ := ok.Seek(0, io.SeekCurrent); pos != 0 {
		t.Errorf("reader not rewound: at %d", pos)
	}
}

func TestDecodeImageRefusesHugeFile(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_PIXELS", "1000000")
	p := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(p, headerOnlyPNG(12000, 12000), 0o644) // 144 MP declared
	if _, err := decodeImage(p); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestEmbeddedHugeImageIsSkippedNotFatal(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_PIXELS", "1000000")
	p := buildZip(t, "x.docx", func(zw *zip.Writer) {
		w, _ := zw.Create("word/media/huge.png")
		_, _ = w.Write(headerOnlyPNG(12000, 12000))
		w, _ = zw.Create("word/media/fine.png")
		var b bytes.Buffer
		_ = png.Encode(&b, solid(400, 300))
		_, _ = w.Write(b.Bytes())
	})
	got, err := collectEmbedded(p, "word/media")
	if err != nil || len(got) != 1 || got[0].Name != "fine.png" {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		is   error
	}{
		{"zip", fmt.Errorf("open: %w", zip.ErrFormat), ErrCorruptInput},
		{"image", fmt.Errorf("decode: %w", image.ErrFormat), ErrCorruptInput},
		{"truncated", io.ErrUnexpectedEOF, ErrCorruptInput},
		{"anydoc missing", errAnyDocNotAvailable, ErrBackendUnavailable},
		{"not found", &exec.Error{Name: "pdftotext", Err: exec.ErrNotFound}, ErrBackendUnavailable},
	}
	for _, c := range cases {
		if got := classifyError(c.err); !errors.Is(got, c.is) || !errors.Is(got, c.err) {
			t.Errorf("%s: %v does not wrap both %v and the cause", c.name, got, c.is)
		}
	}
	if err := errors.New("other"); classifyError(err) != err {
		t.Error("an unrecognised error must pass through unchanged")
	}
}

func TestConvertTruncatedDocxIsCorrupt(t *testing.T) {
	full := buildZip(t, "a.docx", func(zw *zip.Writer) {
		w, _ := zw.Create("word/document.xml")
		_, _ = w.Write([]byte("<w:document/>"))
	})
	b, _ := os.ReadFile(full)
	cut := filepath.Join(t.TempDir(), "cut.docx")
	_ = os.WriteFile(cut, b[:len(b)/2], 0o644)
	_, _, err := (&EIFDoclingEngine{}).Convert(t.Context(), cut)
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v", err)
	}
}

func TestBackendStatusUsesLookups(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLEANN_SOFFICE", "")
	t.Setenv("GLEANN_DOCLING_VLM", "0")
	calls := 0
	have := map[string]bool{"pdftotext": true, "pdftoppm": true}
	eng := &EIFDoclingEngine{lookPath: func(n string) (string, error) {
		calls++
		if have[n] {
			return "/bin/" + n, nil
		}
		return "", exec.ErrNotFound
	}}
	st := eng.BackendStatus()
	if !st["pdftotext"] || !st["pdftoppm"] || st["libreoffice"] || st["anydoc"] {
		t.Fatalf("status %v", st)
	}
	first := calls
	_ = eng.BackendStatus()
	if calls != first {
		t.Errorf("lookups repeated within the cache window: %d then %d", first, calls)
	}
	missing := eng.MissingFormats()
	has := func(s string) bool {
		for _, m := range missing {
			if m == s {
				return true
			}
		}
		return false
	}
	if has(".pdf") || !has(".pdf (scanned)") || !has(".odt") || !has(".doc") {
		t.Errorf("missing = %v", missing)
	}

	have = map[string]bool{}
	eng2 := &EIFDoclingEngine{lookPath: func(n string) (string, error) { return "", exec.ErrNotFound }}
	if m := eng2.MissingFormats(); len(m) == 0 || !contains(m, ".pdf") {
		t.Errorf("without pdftotext, .pdf must be listed: %v", m)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestSweepLegacyTempOnlyOwnStaleNames(t *testing.T) {
	root := privateDir(t)
	old := time.Now().Add(-5 * time.Hour)
	mk := func(name string, age time.Time, dir bool) {
		p := filepath.Join(root, name)
		if dir {
			_ = os.MkdirAll(filepath.Join(p, "sub"), 0o755)
		} else {
			_ = os.WriteFile(p, []byte("x"), 0o644)
		}
		_ = os.Chtimes(p, age, age)
	}
	mk("gleann-img-1.png", old, false)
	mk("gleann_deep_1.png", old, false)
	mk("gleann_docling_pdf_1", old, true)
	mk("gleann-legacy-1", old, true)
	mk("gleann-img-fresh.png", time.Now(), false)
	mk("gleann_upload_server.docx", old, false) // the gleann server's own prefix
	mk("unrelated.txt", old, false)
	if n := SweepLegacyTemp(root, 2*time.Hour); n != 4 {
		t.Errorf("removed %d, want 4", n)
	}
	for _, keep := range []string{"gleann-img-fresh.png", "gleann_upload_server.docx", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
}

func TestResetTempDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gleann-plugin-docs", "8765")
	_ = os.MkdirAll(filepath.Join(dir, "leftover", "x"), 0o755)
	if err := ResetTempDir(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("not empty: %v", entries)
	}
}

func TestBoundedEntryCutsOverlongStream(t *testing.T) {
	b := &boundedEntry{rc: io.NopCloser(bytes.NewReader(make([]byte, 5000))), left: 1000, name: "a.xml"}
	n, err := io.Copy(io.Discard, b)
	if !errors.Is(err, ErrTooLarge) || n != 1000 {
		t.Fatalf("read %d bytes, err %v; want 1000 and ErrTooLarge", n, err)
	}
}
