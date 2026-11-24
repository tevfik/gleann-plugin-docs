package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func addFile(zw *zip.Writer, name, body string) {
	w, _ := zw.Create(name)
	_, _ = w.Write([]byte(body))
}

// pptxSlideXML is a slide whose text is about size bytes long.
func pptxSlideXML(size int) string {
	var sb strings.Builder
	sb.WriteString(`<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree>`)
	line := strings.Repeat("x", 100)
	for sb.Len() < size {
		sb.WriteString(`<p:sp><p:txBody><a:p><a:r><a:t>` + line + `</a:t></a:r></a:p></p:txBody></p:sp>`)
	}
	sb.WriteString(`</p:spTree></p:cSld></p:sld>`)
	return sb.String()
}

func pptxWith(t *testing.T, slides int, refs func(i int) string, slideSize int) string {
	t.Helper()
	return buildZip(t, "x.pptx", func(zw *zip.Writer) {
		var pres, rels strings.Builder
		pres.WriteString(`<p:presentation xmlns:p="p" xmlns:r="r"><p:sldIdLst>`)
		rels.WriteString(`<Relationships>`)
		for i := 1; i <= slides; i++ {
			rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Target="slides/slide%d.xml"/>`, i, i))
			addFile(zw, fmt.Sprintf("ppt/slides/slide%d.xml", i), pptxSlideXML(slideSize))
		}
		for i := 0; ; i++ {
			r := refs(i)
			if r == "" {
				break
			}
			pres.WriteString(`<p:sldId id="256" r:id="` + r + `"/>`)
		}
		pres.WriteString(`</p:sldIdLst></p:presentation>`)
		rels.WriteString(`</Relationships>`)
		addFile(zw, "ppt/presentation.xml", pres.String())
		addFile(zw, "ppt/_rels/presentation.xml.rels", rels.String())
	})
}

// F1: hundreds of references to one slide must not repeat it.
func TestPPTXRepeatedSlideReferenceIsReadOnce(t *testing.T) {
	const size = 256 << 10
	p := pptxWith(t, 1, func(i int) string {
		if i < 300 {
			return "rId1"
		}
		return ""
	}, size)
	md, n, err := ExtractPPTX(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(md, "## Slide"); got != 1 || n != 1 {
		t.Errorf("slide printed %d times, count %d; want once", got, n)
	}
	if len(md) > 2*size {
		t.Errorf("%d bytes of Markdown from a %d byte slide", len(md), size)
	}
}

// F1: the Markdown cap applies while the result is built.
func TestPPTXOutputCapAppliesWhileBuilding(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_MARKDOWN_BYTES", "100000")
	p := pptxWith(t, 40, func(i int) string {
		if i < 40 {
			return fmt.Sprintf("rId%d", i+1)
		}
		return ""
	}, 30<<10)
	if _, _, err := ExtractPPTX(p); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestMDBuilderStopsAtLimit(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_MARKDOWN_BYTES", "1000")
	b := newMDBuilder()
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(b, "%0100d", i)
	}
	if b.Len() > 1000 {
		t.Errorf("builder grew to %d bytes, limit 1000", b.Len())
	}
	if !errors.Is(b.Err(), ErrTooLarge) {
		t.Errorf("Err = %v", b.Err())
	}
}

// F1: every read of a member is charged, so reading it twice costs twice.
func TestZipBudgetChargesEveryRead(t *testing.T) {
	t.Setenv("GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES", "150000")
	data := bytes.Repeat([]byte("a"), 100000)
	p := buildZip(t, "a.docx", func(zw *zip.Writer) { addFile(zw, "a.xml", string(data)) })
	r, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	budget := newZipBudget()
	read := func() error {
		rc, err := openEntry(budget, r.File[0])
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.Copy(io.Discard, rc)
		return err
	}
	if err := read(); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if err := read(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("second read: got %v, want ErrTooLarge", err)
	}
}

func TestEPUBRepeatedSpineItemIsReadOnce(t *testing.T) {
	const size = 256 << 10
	p := buildZip(t, "x.epub", func(zw *zip.Writer) {
		addFile(zw, "META-INF/container.xml", `<container><rootfiles><rootfile full-path="OEBPS/c.opf"/></rootfiles></container>`)
		var spine strings.Builder
		for i := 0; i < 300; i++ {
			spine.WriteString(`<itemref idref="ch1"/>`)
		}
		addFile(zw, "OEBPS/c.opf", `<package><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine>`+spine.String()+`</spine></package>`)
		addFile(zw, "OEBPS/ch1.xhtml", "<html><body>"+strings.Repeat("<p>"+strings.Repeat("y", 100)+"</p>", size/110)+"</body></html>")
	})
	md, n, err := ExtractEPUB(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(md) > 2*size {
		t.Errorf("%d chapters, %d bytes from a %d byte chapter", n, len(md), size)
	}
}

func xlsxWith(t *testing.T, sheetXML string, sheets int, shared string) string {
	t.Helper()
	return buildZip(t, "x.xlsx", func(zw *zip.Writer) {
		var wb strings.Builder
		wb.WriteString(`<workbook xmlns:r="r"><sheets>`)
		for i := 0; i < sheets; i++ {
			wb.WriteString(fmt.Sprintf(`<sheet name="S%d" sheetId="%d" r:id="rId1"/>`, i, i+1))
		}
		wb.WriteString(`</sheets></workbook>`)
		addFile(zw, "xl/workbook.xml", wb.String())
		addFile(zw, "xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
		addFile(zw, "xl/worksheets/sheet1.xml", sheetXML)
		if shared != "" {
			addFile(zw, "xl/sharedStrings.xml", shared)
		}
	})
}

func TestXLSXRepeatedSheetIsReadOnce(t *testing.T) {
	var rows strings.Builder
	rows.WriteString(`<worksheet><sheetData>`)
	for i := 0; i < 2000; i++ {
		rows.WriteString(`<row><c t="inlineStr"><is><t>` + strings.Repeat("z", 120) + `</t></is></c></row>`)
	}
	rows.WriteString(`</sheetData></worksheet>`)
	md, _, err := ExtractXLSX(xlsxWith(t, rows.String(), 300, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(md, "\n## "); got != 0 || strings.Count(md, "## S") != 1 {
		t.Errorf("sheet printed %d times", strings.Count(md, "## S"))
	}
	if len(md) > 2*2000*130 {
		t.Errorf("%d bytes from a %d byte sheet", len(md), 2000*130)
	}
}

// A few cells that all point at one huge shared string must not render gigabytes.
func TestXLSXSharedStringAmplificationIsCapped(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_MARKDOWN_BYTES", "1000000")
	shared := `<sst><si><t>` + strings.Repeat("q", 500000) + `</t></si></sst>`
	var sh strings.Builder
	sh.WriteString(`<worksheet><sheetData><row>`)
	for i := 0; i < 100; i++ {
		sh.WriteString(`<c t="s"><v>0</v></c>`)
	}
	sh.WriteString(`</row></sheetData></worksheet>`)
	if _, _, err := ExtractXLSX(xlsxWith(t, sh.String(), 1, shared)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

// tiffHeader is a minimal little-endian TIFF that declares w x h pixels.
func tiffHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("II*\x00")
	_ = binary.Write(&b, binary.LittleEndian, uint32(8))
	type ent struct {
		tag, typ uint16
		cnt, val uint32
	}
	ents := []ent{
		{256, 4, 1, w}, {257, 4, 1, h}, {258, 3, 1, 8}, {259, 3, 1, 1}, {262, 3, 1, 1},
		{273, 4, 1, 200}, {277, 3, 1, 1}, {278, 4, 1, h}, {279, 4, 1, 1},
	}
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(ents)))
	for _, e := range ents {
		_ = binary.Write(&b, binary.LittleEndian, e.tag)
		_ = binary.Write(&b, binary.LittleEndian, e.typ)
		_ = binary.Write(&b, binary.LittleEndian, e.cnt)
		_ = binary.Write(&b, binary.LittleEndian, e.val)
	}
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	b.Write(make([]byte, 64))
	return b.Bytes()
}

// F2: 4294967295 x 4294967295 overflows a plain int64 product.
func TestHugeTIFFIsRefusedNotDecoded(t *testing.T) {
	if err := checkImageConfig(bytes.NewReader(tiffHeader(0xFFFFFFFF, 0xFFFFFFFF)), MaxPixels()); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("checkImageConfig: got %v, want ErrImageTooLarge", err)
	}
	p := filepath.Join(t.TempDir(), "huge.tif")
	_ = os.WriteFile(p, tiffHeader(0xFFFFFFFF, 0xFFFFFFFF), 0o644)
	if _, err := decodeImage(p); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("decodeImage: got %v, want ErrImageTooLarge", err)
	}
}

func TestPixelCheckHasNoOverflowAndRejectsEmpty(t *testing.T) {
	for _, wh := range [][2]uint32{{1 << 31, 1 << 31}, {0xFFFFFFFF, 2}, {3_000_000_000, 3_000_000_000}} {
		if err := checkImageConfig(bytes.NewReader(tiffHeader(wh[0], wh[1])), MaxPixels()); !errors.Is(err, ErrImageTooLarge) {
			t.Errorf("%dx%d: got %v", wh[0], wh[1], err)
		}
	}
	if err := checkImageConfig(bytes.NewReader(tiffHeader(0, 10)), MaxPixels()); !errors.Is(err, ErrCorruptInput) {
		t.Errorf("0x10: got %v, want ErrCorruptInput", err)
	}
}

func init() {
	image.RegisterFormat("panicfmt", "PANICFMT", func(io.Reader) (image.Image, error) { panic("hostile image") },
		func(io.Reader) (image.Config, error) { return image.Config{Width: 4, Height: 4}, nil })
}

// F2: a decoder panic is ErrCorruptInput, not a dropped connection.
func TestDecoderPanicBecomesCorruptInput(t *testing.T) {
	_, err := decodeImageSafe(strings.NewReader("PANICFMT-and-some-more"))
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v, want ErrCorruptInput", err)
	}
	p := filepath.Join(t.TempDir(), "x.bin")
	_ = os.WriteFile(p, []byte("PANICFMT-and-some-more"), 0o644)
	if _, err := decodeImage(p); !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("decodeImage: got %v, want ErrCorruptInput", err)
	}
}

// --- F3: temporary directory ---

func TestInstanceTempDirIsPerUser(t *testing.T) {
	dir := InstanceTempDir(8765)
	if root := filepath.Base(filepath.Dir(dir)); !strings.HasPrefix(root, "gleann-plugin-docs-") {
		t.Errorf("root %q is not per user", root)
	}
}

func TestResetTempDirRefusesSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	keep := filepath.Join(victim, "8765", "keep")
	_ = os.MkdirAll(filepath.Dir(keep), 0o755)
	_ = os.WriteFile(keep, []byte("x"), 0o644)
	root := filepath.Join(base, "gleann-plugin-docs-1")
	if err := os.Symlink(victim, root); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := ResetTempDir(filepath.Join(root, "8765")); err == nil {
		t.Error("a symlinked root was accepted")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("file behind the symlink was deleted: %v", err)
	}
}

func TestResetTempDirRefusesOpenRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	root := filepath.Join(t.TempDir(), "gleann-plugin-docs-1")
	_ = os.Mkdir(root, 0o755)
	_ = os.Chmod(root, 0o777)
	if err := ResetTempDir(filepath.Join(root, "8765")); err == nil {
		t.Error("a world-writable root was accepted")
	}
}

func TestSweepLegacyTempRefusesSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	_ = os.Mkdir(target, 0o755)
	f := filepath.Join(target, "gleann-img-1.png")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	old := time.Now().Add(-5 * time.Hour)
	_ = os.Chtimes(f, old, old)
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	if n := SweepLegacyTemp(link, time.Hour); n != 0 {
		t.Errorf("swept %d entries through a symlinked root", n)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("file in the symlink's target was removed: %v", err)
	}
}

func TestSweepLegacyTempRefusesOpenRootButAcceptsSticky(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	root := privateDir(t)
	f := filepath.Join(root, "gleann-img-1.png")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	old := time.Now().Add(-5 * time.Hour)
	_ = os.Chtimes(f, old, old)
	_ = os.Chmod(root, 0o777)
	if n := SweepLegacyTemp(root, time.Hour); n != 0 {
		t.Errorf("swept %d entries of a world-writable root without sticky bit", n)
	}
	_ = os.Chmod(root, 0o777|os.ModeSticky)
	if n := SweepLegacyTemp(root, time.Hour); n != 1 {
		t.Errorf("sticky shared temp dir: swept %d, want 1", n)
	}
}

func TestSweepLegacyTempNeverFollowsOrRemovesLinks(t *testing.T) {
	root := privateDir(t)
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	_ = os.WriteFile(keep, []byte("x"), 0o644)
	link := filepath.Join(root, "gleann-legacy-evil")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	SweepLegacyTemp(root, 0)
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("sweep removed a symlink entry: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sweep followed the link: %v", err)
	}
}

// privateDir is a fresh 0700 directory (t.TempDir follows the umask, which may
// leave it group-writable).
func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOwnedByCurrentUser(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a non-root unix user")
	}
	own, _ := os.Lstat(t.TempDir())
	if !ownedByCurrentUser(own) {
		t.Error("own directory reported as foreign")
	}
	root, err := os.Lstat("/")
	if err != nil {
		t.Skip()
	}
	if ownedByCurrentUser(root) {
		t.Error("/ reported as owned by this user")
	}
}

// --- F4, F5: cancellation and error wrapping ---

func fakeTool(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCancelledChildIsReportedAsCancelled(t *testing.T) {
	dir := fakeTool(t, "pdftotext", "sleep 10")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pdf := filepath.Join(t.TempDir(), "a.pdf")
	_ = os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, _, err := (&EIFDoclingEngine{}).Convert(ctx, pdf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled in the chain", err)
	}
	if errors.Is(err, ErrCorruptInput) {
		t.Errorf("a cancelled conversion is reported as corrupt input: %v", err)
	}
}

func TestFailingChildWithoutCancelStaysCorruptInput(t *testing.T) {
	dir := fakeTool(t, "soffice", "exit 1")
	t.Setenv("GLEANN_SOFFICE", filepath.Join(dir, "soffice"))
	doc := filepath.Join(t.TempDir(), "a.doc")
	_ = os.WriteFile(doc, []byte("not a doc"), 0o644)
	_, _, err := (&EIFDoclingEngine{}).Convert(context.Background(), doc)
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v, want ErrCorruptInput (a LibreOffice exit status)", err)
	}
}

func TestLibreOfficeTimeoutReachesDeadlineMapping(t *testing.T) {
	dir := fakeTool(t, "soffice", "sleep 10")
	t.Setenv("GLEANN_SOFFICE", filepath.Join(dir, "soffice"))
	doc := filepath.Join(t.TempDir(), "a.doc")
	_ = os.WriteFile(doc, []byte("x"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := (&EIFDoclingEngine{}).Convert(ctx, doc)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded in the chain", err)
	}
}

// /health must answer while a model run holds the engine lock.
func TestBackendStatusDoesNotWaitForModelRun(t *testing.T) {
	e := &EIFDoclingEngine{}
	e.mu.Lock()
	defer e.mu.Unlock()
	done := make(chan map[string]bool, 1)
	go func() { done <- e.BackendStatus() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BackendStatus blocked behind the engine lock")
	}
}

// --- re-review R1: sparse xlsx references ---

func sheetWith(t *testing.T, body string) string {
	t.Helper()
	return xlsxWith(t, `<worksheet><sheetData>`+body+`</sheetData></worksheet>`, 1, "")
}

func allocated() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.TotalAlloc
}

func TestXLSXColumnPastXFDIsRefused(t *testing.T) {
	for _, ref := range []string{"XFE1", "ZZZZZ1", "ZZZZZZZ1", strings.Repeat("Z", 40) + "1"} {
		before := allocated()
		_, _, err := ExtractXLSX(sheetWith(t, `<row><c r="`+ref+`" t="inlineStr"><is><t>x</t></is></c></row>`))
		if !errors.Is(err, ErrCorruptInput) {
			t.Errorf("%s: got %v, want ErrCorruptInput", ref, err)
		}
		if d := allocated() - before; d > 50<<20 {
			t.Errorf("%s: %d MB allocated", ref, d>>20)
		}
	}
}

func TestXLSXRowPastLimitIsRefused(t *testing.T) {
	_, _, err := ExtractXLSX(sheetWith(t, `<row r="1048577"><c t="inlineStr"><is><t>x</t></is></c></row>`))
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v, want ErrCorruptInput", err)
	}
	if _, _, err := ExtractXLSX(sheetWith(t, `<row r="1048576"><c t="inlineStr"><is><t>x</t></is></c></row>`)); err != nil {
		t.Fatalf("last valid row: %v", err)
	}
}

// A valid far column on many rows pads every row to 16384 cells; the padding is
// charged against the Markdown cap, so the sheet is stopped early.
func TestXLSXPaddingIsChargedAgainstTheCap(t *testing.T) {
	var rows strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&rows, `<row><c r="XFD%d" t="inlineStr"><is><t>x</t></is></c></row>`, i)
	}
	before := allocated()
	_, _, err := ExtractXLSX(sheetWith(t, rows.String()))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	// The cap is 64 MiB of charged cells; without padding accounting this
	// allocates 20000 x 16384 x 16 bytes = 5 GB.
	if d := allocated() - before; d > 400<<20 {
		t.Errorf("%d MB allocated before the cap stopped the sheet", d>>20)
	}
}

// --- re-review R2: embedded images one at a time ---

func collectEmbedded(zipPath, mediaDir string) ([]embeddedImage, error) {
	var out []embeddedImage
	err := forEachEmbeddedImage(zipPath, mediaDir, func(im embeddedImage) bool {
		out = append(out, im)
		return true
	})
	return out, err
}

func pngOf(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestEmbeddedImagesAreNotAllResident(t *testing.T) {
	const n, side = 10, 3000 // 36 MB decoded each, a few KB as PNG
	data := pngOf(image.NewNRGBA(image.Rect(0, 0, side, side)))
	p := buildZip(t, "x.docx", func(zw *zip.Writer) {
		for i := 0; i < n; i++ {
			addFile(zw, fmt.Sprintf("word/media/image%02d.png", i), string(data))
		}
	})
	var peak uint64
	count := 0
	err := forEachEmbeddedImage(p, "word/media", func(embeddedImage) bool {
		count++
		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		if m.HeapAlloc > peak {
			peak = m.HeapAlloc
		}
		return true
	})
	if err != nil || count != n {
		t.Fatalf("count %d err %v", count, err)
	}
	// Ten resident images would be 360 MB; one at a time stays far below.
	if peak > 150<<20 {
		t.Errorf("heap peaked at %d MB: images are held together", peak>>20)
	}
}

func TestEmbeddedImageReadsAreChargedToTheBudget(t *testing.T) {
	rng := image.NewNRGBA(image.Rect(0, 0, 220, 220))
	seed := uint32(1)
	for i := range rng.Pix {
		seed = seed*1664525 + 1013904223
		rng.Pix[i] = byte(seed >> 24)
	}
	data := pngOf(rng) // about 190 KB of incompressible pixels
	p := buildZip(t, "x.docx", func(zw *zip.Writer) {
		for i := 0; i < 8; i++ {
			addFile(zw, fmt.Sprintf("word/media/image%d.png", i), string(data))
		}
	})
	t.Setenv("GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES", fmt.Sprint(3*len(data)))
	count := 0
	err := forEachEmbeddedImage(p, "word/media", func(embeddedImage) bool { count++; return true })
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v, want ErrTooLarge", err)
	}
	if count >= 8 {
		t.Errorf("read all %d images despite the budget", count)
	}
}

// --- third pass T1: padding a table to its widest row ---

// bounded runs fn and fails unless it ends with want and allocates little.
func bounded(t *testing.T, name string, want error, fn func() error) {
	t.Helper()
	boundedMB(t, name, want, 300, fn)
}

func boundedMB(t *testing.T, name string, want error, maxMB uint64, fn func() error) {
	t.Helper()
	before := allocated()
	err := fn()
	if !errors.Is(err, want) {
		t.Errorf("%s: got %v, want %v", name, err, want)
	}
	if d := allocated() - before; !raceEnabled && d > maxMB<<20 {
		t.Errorf("%s: %d MB allocated", name, d>>20)
	}
}

func docxTables(t *testing.T, wide, short int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<w:document xmlns:w="w"><w:body><w:tbl><w:tr>`)
	b.WriteString(strings.Repeat(`<w:tc><w:p/></w:tc>`, wide))
	b.WriteString(`</w:tr>`)
	b.WriteString(strings.Repeat(`<w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc></w:tr>`, short))
	b.WriteString(`</w:tbl></w:body></w:document>`)
	return buildZip(t, "t.docx", func(zw *zip.Writer) { addFile(zw, "word/document.xml", b.String()) })
}

func TestDocxWideTableIsBounded(t *testing.T) {
	bounded(t, "30000 wide", ErrCorruptInput, func() error { _, err := ExtractDocx(docxTables(t, 30000, 30000)); return err })
	bounded(t, "16000 wide x 15000 rows", ErrTooLarge, func() error { _, err := ExtractDocx(docxTables(t, 16000, 15000)); return err })
}

func TestXLSXWideTableIsBounded(t *testing.T) {
	var rows strings.Builder
	rows.WriteString(`<row><c r="XFD1" t="inlineStr"><is><t>x</t></is></c></row>`)
	for i := 2; i <= 20001; i++ {
		fmt.Fprintf(&rows, `<row><c r="A%d" t="inlineStr"><is><t>%d</t></is></c></row>`, i, i)
	}
	bounded(t, "XFD1 + 20000 rows", ErrTooLarge, func() error {
		_, _, err := ExtractXLSX(sheetWith(t, rows.String()))
		return err
	})
}

func TestHTMLWideTableIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("<table><tr>" + strings.Repeat("<td></td>", 8000) + "</tr>")
	b.WriteString(strings.Repeat("<tr><td>a</td></tr>", 8000) + "</table>")
	bounded(t, "8000 x 8000", ErrTooLarge, func() error { _, err := ExtractHTML(strings.NewReader(b.String())); return err })
	var w strings.Builder
	w.WriteString("<table><tr>" + strings.Repeat("<td></td>", 30000) + "</tr></table>")
	bounded(t, "30000 wide", ErrCorruptInput, func() error { _, err := ExtractHTML(strings.NewReader(w.String())); return err })
}

func pptxWideTable(t *testing.T, wide, short int) string {
	t.Helper()
	cell := func(txt string) string {
		return `<a:tc><a:txBody><a:p><a:r><a:t>` + txt + `</a:t></a:r></a:p></a:txBody></a:tc>`
	}
	var b strings.Builder
	b.WriteString(`<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree><a:tbl><a:tr>`)
	b.WriteString(strings.Repeat(cell(""), wide))
	b.WriteString(`</a:tr>`)
	b.WriteString(strings.Repeat(`<a:tr>`+cell("a")+`</a:tr>`, short))
	b.WriteString(`</a:tbl></p:spTree></p:cSld></p:sld>`)
	return buildZip(t, "t.pptx", func(zw *zip.Writer) { addFile(zw, "ppt/slides/slide1.xml", b.String()) })
}

func TestPPTXWideTableIsBounded(t *testing.T) {
	bounded(t, "30000 wide", ErrCorruptInput, func() error { _, _, err := ExtractPPTX(pptxWideTable(t, 30000, 30000)); return err })
	bounded(t, "16000 wide x 15000 rows", ErrTooLarge, func() error { _, _, err := ExtractPPTX(pptxWideTable(t, 16000, 15000)); return err })
}

func TestWriteTableProjectsBeforeWriting(t *testing.T) {
	rows := [][]string{make([]string, 10000)}
	for i := 0; i < 10000; i++ {
		rows = append(rows, []string{"a"})
	}
	var sb strings.Builder
	if err := writeTable(&sb, rows, 1<<20, tableOpts{}); !errors.Is(err, ErrTooLarge) || sb.Len() != 0 {
		t.Fatalf("err %v, wrote %d bytes before refusing", err, sb.Len())
	}
	sb.Reset()
	if err := writeTable(&sb, [][]string{{"a", "b"}, {"c"}}, 1<<20, tableOpts{}); err != nil || sb.String() != "| a | b |\n| --- | --- |\n| c |  |" {
		t.Fatalf("got %q err %v", sb.String(), err)
	}
}

// --- fourth pass U1: one output budget per conversion ---

func pptxTables(t *testing.T, tables, wide, short int) string {
	t.Helper()
	cell := func(txt string) string {
		return `<a:tc><a:txBody><a:p><a:r><a:t>` + txt + `</a:t></a:r></a:p></a:txBody></a:tc>`
	}
	one := `<a:tbl><a:tr>` + strings.Repeat(cell(""), wide) + `</a:tr>` + strings.Repeat(`<a:tr>`+cell("a")+`</a:tr>`, short) + `</a:tbl>`
	body := `<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree>` + strings.Repeat(one, tables) + `</p:spTree></p:cSld></p:sld>`
	return buildZip(t, "t.pptx", func(zw *zip.Writer) { addFile(zw, "ppt/slides/slide1.xml", body) })
}

// Each table alone fits under the 64 MiB cap (about 60 MB); twelve of them in one
// slide must still stop at the cap, not at twelve caps.
func TestPPTXManyTablesShareOneBudget(t *testing.T) {
	p := pptxTables(t, 12, 16000, 1250)
	boundedMB(t, "12 tables on one slide", ErrTooLarge, 700, func() error { _, _, err := ExtractPPTX(p); return err })
}

func htmlTables(n int, wrap string) string {
	table := "<table><tr>" + strings.Repeat("<td></td>", 16000) + "</tr>" + strings.Repeat("<tr><td>a</td></tr>", 1250) + "</table>"
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch wrap {
		case "li":
			b.WriteString("<ul><li>" + table + "</li></ul>")
		case "td":
			b.WriteString("<table><tr><td>" + table + "</td></tr></table>")
		}
	}
	return b.String()
}

func TestHTMLNestedTablesShareOneBudget(t *testing.T) {
	for _, wrap := range []string{"li", "td"} {
		doc := htmlTables(12, wrap)
		boundedMB(t, "12 tables in "+wrap, ErrTooLarge, 700, func() error { _, err := ExtractHTML(strings.NewReader(doc)); return err })
	}
}

func biffRec(id uint16, body []byte) []byte {
	b := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint16(b, id)
	binary.LittleEndian.PutUint16(b[2:], uint16(len(body)))
	return append(b, body...)
}

// xlsStream builds a BIFF8 workbook with the given BOUNDSHEET offsets (indexes
// into sheets) and sheet bodies of 65536 rows, one NUMBER in column 255 each
// (the sheet renders to about 50 MB of padded table).
func xlsStream(refs []int, sheets int) []byte {
	bof := func(typ uint16) []byte {
		body := make([]byte, 16)
		binary.LittleEndian.PutUint16(body, 0x0600)
		binary.LittleEndian.PutUint16(body[2:], typ)
		return biffRec(0x0809, body)
	}
	eof := biffRec(0x000A, nil)
	boundLen := len(biffRec(0x0085, make([]byte, 9)))
	globals := len(bof(5)) + len(refs)*boundLen + len(eof)
	var sheetBytes [][]byte
	for s := 0; s < sheets; s++ {
		var sb bytes.Buffer
		sb.Write(bof(0x10))
		num := make([]byte, 14)
		binary.LittleEndian.PutUint16(num[2:], 255)
		binary.LittleEndian.PutUint64(num[6:], 0x3FF0000000000000) // 1.0
		for r := 0; r < 65536; r++ {
			binary.LittleEndian.PutUint16(num, uint16(r))
			sb.Write(biffRec(0x0203, num))
		}
		sb.Write(eof)
		sheetBytes = append(sheetBytes, sb.Bytes())
	}
	offs := make([]int, sheets)
	pos := globals
	for s := range sheetBytes {
		offs[s] = pos
		pos += len(sheetBytes[s])
	}
	var out bytes.Buffer
	out.Write(bof(5))
	for _, r := range refs {
		body := make([]byte, 6, 9)
		binary.LittleEndian.PutUint32(body, uint32(offs[r]))
		body = append(body, 1, 0, 'S') // name: length 1, 8-bit, "S"
		out.Write(biffRec(0x0085, body))
	}
	out.Write(eof)
	for _, s := range sheetBytes {
		out.Write(s)
	}
	return out.Bytes()
}

func TestXLSDuplicateSheetRecordsRenderOnce(t *testing.T) {
	refs := make([]int, 60)
	stream := xlsStream(refs, 1)
	before := allocated()
	md, _, err := parseBIFF8(newOutBudget(), stream)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(md, "## "); got != 1 {
		t.Errorf("sheet rendered %d times", got)
	}
	if d := allocated() - before; d > 1600<<20 { // the BIFF parse of 65536 rows is itself ~800 MB
		t.Errorf("%d MB allocated", d>>20)
	}
}

func TestXLSDistinctSheetsShareOneBudget(t *testing.T) {
	stream := xlsStream([]int{0, 1, 2, 3}, 4)
	boundedMB(t, "4 sheets of ~50 MB", ErrTooLarge, 2400, func() error {
		_, _, err := parseBIFF8(newOutBudget(), stream)
		return err
	})
}

// Pages of a PDF are charged to the same per-document budget: twelve pages of
// 8 MB each (96 MB of text layer) stop at the 64 MiB cap.
func TestPDFPagesShareOneBudget(t *testing.T) {
	script := `i=0; while [ $i -lt 12 ]; do yes "lorem ipsum dolor sit amet consectetur" | head -c 8000000; printf '\f'; i=$((i+1)); done`
	dir := fakeTool(t, "pdftotext", script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pdf := filepath.Join(t.TempDir(), "a.pdf")
	_ = os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o644)
	boundedMB(t, "12 pages of 8 MB", ErrTooLarge, 1500, func() error {
		_, _, err := (&EIFDoclingEngine{}).Convert(context.Background(), pdf)
		return err
	})
}

// A list level from the document is clamped before it becomes an indent: a
// 243-byte docx and a 285-byte pptx used to allocate gigabytes for one item.
func TestDocxListLevelIsBounded(t *testing.T) {
	for _, lvl := range []string{"1500000000", "-5"} {
		p := buildZip(t, "l.docx", func(zw *zip.Writer) {
			addFile(zw, "word/document.xml", `<w:document xmlns:w="w"><w:body><w:p><w:pPr><w:numPr><w:ilvl w:val="`+lvl+`"/></w:numPr></w:pPr><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`)
		})
		before := allocated()
		md, err := ExtractDocx(p)
		if err != nil || !strings.Contains(md, "- x") || len(md) > 64 {
			t.Errorf("ilvl %s: got %q, %v", lvl, md, err)
		}
		if d := allocated() - before; !raceEnabled && d > 50<<20 {
			t.Errorf("ilvl %s: %d MB allocated", lvl, d>>20)
		}
	}
}

func TestPPTXListLevelIsBounded(t *testing.T) {
	for _, lvl := range []string{"1500000000", "-5"} {
		p := buildZip(t, "l.pptx", func(zw *zip.Writer) {
			addFile(zw, "ppt/slides/slide1.xml", `<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree><a:p><a:pPr lvl="`+lvl+`"><a:buChar char="-"/></a:pPr><a:r><a:t>x</a:t></a:r></a:p></p:spTree></p:cSld></p:sld>`)
		})
		before := allocated()
		md, _, err := ExtractPPTX(p)
		if err != nil || !strings.Contains(md, "- x") || len(md) > 128 {
			t.Errorf("lvl %s: got %q, %v", lvl, md, err)
		}
		if d := allocated() - before; !raceEnabled && d > 50<<20 {
			t.Errorf("lvl %s: %d MB allocated", lvl, d>>20)
		}
	}
}
