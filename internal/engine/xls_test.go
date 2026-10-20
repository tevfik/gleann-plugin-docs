package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures were written by LibreOffice (testdata/xls): benchmark_data.xls is benchmark_data.xlsx saved
// as .xls; rich.xls has strings (narrow and wide, enough of them to span SST CONTINUE records), ints,
// floats, dates, error cells and two sheets; formulas.xls has formulas with cached numeric/text/error results.
const xlsDir = "../../testdata/xls/"

// The native reader must give exactly the markdown of the OOXML reader on the same workbook.
func TestExtractXLSMatchesXLSX(t *testing.T) {
	want, wantSheets, err := ExtractXLSX("../../testdata/documents/benchmark_data.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	got, sheets, err := ExtractXLS(xlsDir + "benchmark_data.xls")
	if err != nil {
		t.Fatal(err)
	}
	if sheets != wantSheets || got != want {
		t.Errorf("xls != xlsx (sheets %d vs %d)\n--- xls ---\n%s\n--- xlsx ---\n%s", sheets, wantSheets, got, want)
	}
}

func TestExtractXLSValueTypes(t *testing.T) {
	md, sheets, err := ExtractXLS(xlsDir + "rich.xls")
	if err != nil {
		t.Fatal(err)
	}
	if sheets != 2 || !strings.Contains(md, "## Data") || !strings.Contains(md, "## Çok Satır") {
		t.Fatalf("sheets=%d, headings missing:\n%.300s", sheets, md)
	}
	for _, want := range []string{
		"| Name | Count | Ratio | Flag | Day | Double | Label |",
		"| alpha | 768 | 0.125 | 1 | 2026-10-20 | #VALUE! |", // int, float, date (768 stays 768), error cell; LibreOffice stores booleans as 1/0
		"| beta | -12 | 1234567.891 | 0 | 2024-02-29 |",
		"| Şişli ğüöçı İ | 100000 | 0.3333333333333333 | 1 | 1999-12-31 |", // wide chars
		"| gap |  | 5 |", // sparse cells stay in their column
		"a\\|b",          // pipe is escaped
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q", want)
		}
	}
	// 900 unique strings overflow one SST record: the last must survive intact.
	last := "row 899 unique text xrow 899 unique text xrow 899 unique text x"
	if !strings.Contains(md, last+" | 899 |") {
		t.Errorf("last SST string/value wrong or lost; tail:\n%s", md[max(0, len(md)-200):])
	}
	if !strings.Contains(md, "row 7 unique text şrow 7 unique text şrow 7 unique text ş | 7 |") {
		t.Errorf("wide string across CONTINUE broken")
	}
}

func TestExtractXLSFormulaResults(t *testing.T) {
	md, _, err := ExtractXLS(xlsDir + "formulas.xls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| alpha | 768 | 1536 | alpha-x | #DIV/0! |", "| beta | -12.5 | -25 | beta-x |"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	if strings.Contains(md, "FormulaCol") {
		t.Error("formula placeholder leaked")
	}
}

// .xls must not need LibreOffice: point GLEANN_SOFFICE at nothing and PATH at an empty dir.
func TestConvertXLSWithoutLibreOffice(t *testing.T) {
	t.Setenv("GLEANN_SOFFICE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("PATH", t.TempDir())
	md, sheets, err := (&EIFDoclingEngine{}).Convert(context.Background(), xlsDir+"rich.xls")
	if err != nil || md == "" || sheets != 2 {
		t.Fatalf("Convert .xls without LibreOffice: sheets=%d err=%v", sheets, err)
	}
}

// Truncated or garbage input must be an error or partial text, never a panic.
func TestExtractXLSMalformed(t *testing.T) {
	raw, err := os.ReadFile(xlsDir + "rich.xls")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"garbage": []byte("this is not a workbook"), "empty": {}}
	for _, n := range []int{512, 1024, len(raw) / 3, len(raw) / 2, len(raw) - 100} {
		cases["truncated"+string(rune('a'+n%26))] = raw[:n]
	}
	corrupt := append([]byte(nil), raw...)
	for i := 2000; i < len(corrupt); i += 97 {
		corrupt[i] ^= 0xA5
	}
	cases["bitflips"] = corrupt
	for name, data := range cases {
		p := filepath.Join(t.TempDir(), name+".xls")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		ExtractXLS(p) // must return, not panic (recovered inside)
	}
}
