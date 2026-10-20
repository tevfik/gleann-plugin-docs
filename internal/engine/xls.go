package engine

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/extrame/ole2"
)

// Pure-Go reader for legacy BIFF8 workbooks (.xls, Excel 97-2003), so these files need no
// LibreOffice. Only the OLE2 container comes from a library (extrame/ole2); the BIFF records
// are parsed here because the available xls libraries render every custom-format number as a
// date and return a placeholder for formula cells.
//
// Cells are rendered like ExtractXLSX: strings as stored, numbers shortest-round-trip, booleans
// TRUE/FALSE, formulas as their cached result. Cells with a date/time number format are written
// as ISO dates. Not supported: password-protected workbooks and BIFF5 or older (both an error).

const (
	biffBOF       = 0x0809
	biffEOF       = 0x000A
	biffContinue  = 0x003C
	biffBoundSh   = 0x0085
	biffSST       = 0x00FC
	biffXF        = 0x00E0
	biffFormat    = 0x041E
	biffDate1904  = 0x0022
	biffFilePass  = 0x002F
	biffLabelSST  = 0x00FD
	biffLabel     = 0x0204
	biffNumber    = 0x0203
	biffRK        = 0x027E
	biffMulRK     = 0x00BD
	biffBoolErr   = 0x0205
	biffFormula   = 0x0006
	biffString    = 0x0207
	maxXLSRows    = 65536
	maxXLSColumns = 256
)

type biffRecord struct {
	id     uint16
	off    int
	chunks [][]byte // chunks[0] is the record body, the rest are CONTINUE bodies
}

func (r *biffRecord) body() []byte { return r.chunks[0] }

// ExtractXLS converts a legacy .xls workbook to markdown, one table per worksheet.
func ExtractXLS(filePath string) (md string, sheets int, err error) {
	defer func() {
		if r := recover(); r != nil {
			md, sheets, err = "", 0, fmt.Errorf("xls: malformed workbook: %v", r)
		}
	}()
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	o, err := ole2.Open(f, "utf-8")
	if err != nil {
		return "", 0, fmt.Errorf("xls: not an OLE2 file: %w", err)
	}
	dir, err := o.ListDir()
	if err != nil {
		return "", 0, fmt.Errorf("xls: %w", err)
	}
	var book, root *ole2.File
	for _, d := range dir {
		switch d.Name() {
		case "Workbook", "Book":
			book = d
		case "Root Entry":
			root = d
		}
	}
	if book == nil {
		return "", 0, fmt.Errorf("xls: no Workbook stream (BIFF5 or not an Excel file)")
	}
	stream, err := io.ReadAll(o.OpenFile(book, root))
	if err != nil {
		return "", 0, fmt.Errorf("xls: %w", err)
	}
	return parseBIFF8(stream)
}

func readBIFFRecords(b []byte) []biffRecord {
	var recs []biffRecord
	for pos := 0; pos+4 <= len(b); {
		id := binary.LittleEndian.Uint16(b[pos:])
		n := int(binary.LittleEndian.Uint16(b[pos+2:]))
		if pos+4+n > len(b) {
			break
		}
		data := b[pos+4 : pos+4+n]
		if id == biffContinue && len(recs) > 0 {
			last := &recs[len(recs)-1]
			last.chunks = append(last.chunks, data)
		} else {
			recs = append(recs, biffRecord{id: id, off: pos, chunks: [][]byte{data}})
		}
		pos += 4 + n
	}
	return recs
}

type xlsSheetInfo struct {
	name string
	off  int
	kind byte
}

type xlsBook struct {
	sst      []string
	xfFormat []uint16
	formats  map[uint16]string
	date1904 bool
	sheets   []xlsSheetInfo
}

func parseBIFF8(stream []byte) (string, int, error) {
	recs := readBIFFRecords(stream)
	if len(recs) == 0 || recs[0].id != biffBOF || len(recs[0].body()) < 2 {
		return "", 0, fmt.Errorf("xls: no BIFF stream")
	}
	if v := binary.LittleEndian.Uint16(recs[0].body()); v != 0x0600 {
		return "", 0, fmt.Errorf("xls: BIFF version 0x%04x not supported (only Excel 97-2003)", v)
	}
	bk := &xlsBook{formats: map[uint16]string{}}
	byOff := map[int]int{}
	for i, r := range recs {
		byOff[r.off] = i
	}

	// Workbook globals: everything up to the first EOF.
	for _, r := range recs[1:] {
		if r.id == biffEOF {
			break
		}
		d := r.body()
		switch r.id {
		case biffFilePass:
			return "", 0, fmt.Errorf("xls: workbook is password protected")
		case biffDate1904:
			bk.date1904 = len(d) >= 2 && binary.LittleEndian.Uint16(d) == 1
		case biffSST:
			bk.sst = readSST(r.chunks)
		case biffXF:
			if len(d) >= 4 {
				bk.xfFormat = append(bk.xfFormat, binary.LittleEndian.Uint16(d[2:]))
			}
		case biffFormat:
			if len(d) >= 5 {
				s, _ := readUnicodeString(d[2:], false)
				bk.formats[binary.LittleEndian.Uint16(d)] = s
			}
		case biffBoundSh:
			if len(d) >= 8 {
				name, _ := readUnicodeString(d[6:], true)
				bk.sheets = append(bk.sheets, xlsSheetInfo{name, int(binary.LittleEndian.Uint32(d)), d[5]})
			}
		}
	}

	var sb strings.Builder
	for i, si := range bk.sheets {
		if si.kind != 0 { // worksheets only, no charts or macro sheets
			continue
		}
		start, ok := byOff[si.off]
		if !ok {
			continue
		}
		table := rowsToMarkdown(bk.readSheet(recs[start:]))
		if strings.TrimSpace(table) == "" {
			continue
		}
		name := si.name
		if name == "" {
			name = fmt.Sprintf("Sheet %d", i+1)
		}
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", name, table)
	}
	return strings.TrimSpace(sb.String()), len(bk.sheets), nil
}

// readSheet collects the cells of the worksheet whose BOF is recs[0].
func (bk *xlsBook) readSheet(recs []biffRecord) [][]string {
	cells := map[int]map[int]string{}
	maxCol := -1
	set := func(r, c int, v string) {
		if r >= maxXLSRows || c >= maxXLSColumns || v == "" {
			return
		}
		if cells[r] == nil {
			cells[r] = map[int]string{}
		}
		cells[r][c] = v
		if c > maxCol {
			maxCol = c
		}
	}
	u16 := func(d []byte, o int) int { return int(binary.LittleEndian.Uint16(d[o:])) }
	depth := 0
	pending := [2]int{-1, -1} // cell of a formula waiting for its STRING record
	for _, r := range recs {
		d := r.body()
		switch r.id {
		case biffBOF:
			depth++
			continue
		case biffEOF:
			depth--
			if depth <= 0 {
				return sparseRows(cells, maxCol)
			}
			continue
		}
		if depth != 1 { // nested streams (embedded charts)
			continue
		}
		switch r.id {
		case biffLabelSST:
			if len(d) >= 10 {
				if i := int(binary.LittleEndian.Uint32(d[6:])); i < len(bk.sst) {
					set(u16(d, 0), u16(d, 2), bk.sst[i])
				}
			}
		case biffLabel:
			if len(d) >= 9 {
				s, _ := readUnicodeString(d[6:], false)
				set(u16(d, 0), u16(d, 2), s)
			}
		case biffNumber:
			if len(d) >= 14 {
				set(u16(d, 0), u16(d, 2), bk.number(u16(d, 4), math.Float64frombits(binary.LittleEndian.Uint64(d[6:]))))
			}
		case biffRK:
			if len(d) >= 10 {
				set(u16(d, 0), u16(d, 2), bk.number(u16(d, 4), rkValue(binary.LittleEndian.Uint32(d[6:]))))
			}
		case biffMulRK:
			if len(d) >= 6 {
				row, first := u16(d, 0), u16(d, 2)
				for o, c := 4, first; o+6 <= len(d)-2; o, c = o+6, c+1 {
					set(row, c, bk.number(u16(d, o), rkValue(binary.LittleEndian.Uint32(d[o+2:]))))
				}
			}
		case biffBoolErr:
			if len(d) >= 8 {
				set(u16(d, 0), u16(d, 2), boolErr(d[6], d[7]))
			}
		case biffFormula:
			if len(d) >= 14 {
				row, col, res := u16(d, 0), u16(d, 2), d[6:14]
				if res[6] == 0xFF && res[7] == 0xFF {
					switch res[0] {
					case 0: // text result follows in a STRING record
						pending = [2]int{row, col}
					case 1:
						set(row, col, boolErr(res[2], 0))
					case 2:
						set(row, col, boolErr(res[2], 1))
					}
				} else {
					set(row, col, bk.number(u16(d, 4), math.Float64frombits(binary.LittleEndian.Uint64(res))))
				}
			}
		case biffString:
			if pending[0] >= 0 {
				s, _ := readUnicodeString(d, false)
				set(pending[0], pending[1], s)
				pending = [2]int{-1, -1}
			}
		}
	}
	return sparseRows(cells, maxCol)
}

func sparseRows(cells map[int]map[int]string, maxCol int) [][]string {
	rowIdx := make([]int, 0, len(cells))
	for r := range cells {
		rowIdx = append(rowIdx, r)
	}
	sort.Ints(rowIdx)
	rows := make([][]string, 0, len(rowIdx))
	for _, r := range rowIdx {
		row := make([]string, maxCol+1)
		for c, v := range cells[r] {
			row[c] = strings.TrimSpace(v)
		}
		if hasContent(row) {
			rows = append(rows, row)
		}
	}
	return rows
}

func rkValue(rk uint32) float64 {
	var v float64
	if rk&2 != 0 {
		v = float64(int32(rk) >> 2)
	} else {
		v = math.Float64frombits(uint64(rk&0xFFFFFFFC) << 32)
	}
	if rk&1 != 0 {
		v /= 100
	}
	return v
}

func boolErr(val, isErr byte) string {
	if isErr == 0 {
		if val != 0 {
			return "TRUE"
		}
		return "FALSE"
	}
	switch val {
	case 0x00:
		return "#NULL!"
	case 0x07:
		return "#DIV/0!"
	case 0x0F:
		return "#VALUE!"
	case 0x17:
		return "#REF!"
	case 0x1D:
		return "#NAME?"
	case 0x24:
		return "#NUM!"
	case 0x2A:
		return "#N/A"
	}
	return "#ERR"
}

// number renders a numeric cell, as an ISO date when its number format is a date/time one.
func (bk *xlsBook) number(xf int, v float64) string {
	if xf < len(bk.xfFormat) && bk.isDateFormat(bk.xfFormat[xf]) && v >= 0 && v < 2958466 {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		if bk.date1904 {
			base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		t := base.Add(time.Duration(math.Round(v*86400)) * time.Second)
		switch {
		case v < 1:
			return t.Format("15:04:05")
		case v == math.Floor(v):
			return t.Format("2006-01-02")
		}
		return t.Format("2006-01-02 15:04:05")
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (bk *xlsBook) isDateFormat(idx uint16) bool {
	switch {
	case idx >= 14 && idx <= 22, idx >= 27 && idx <= 36, idx >= 45 && idx <= 47, idx >= 50 && idx <= 58:
		return true
	case idx < 164:
		return false
	}
	f := bk.formats[idx]
	var in strings.Builder
	quoted, bracket := false, false
	for i := 0; i < len(f); i++ {
		c := f[i]
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '[':
			bracket = true
		case c == ']':
			bracket = false
		case bracket:
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == ';':
			return containsDateToken(in.String())
		default:
			in.WriteByte(c)
		}
	}
	return containsDateToken(in.String())
}

func containsDateToken(s string) bool {
	return strings.ContainsAny(strings.ToLower(s), "dmyhs")
}

// readUnicodeString reads a BIFF8 string: cch (1 or 2 bytes), flags, [rich/phonetic sizes], chars.
func readUnicodeString(b []byte, shortLen bool) (string, int) {
	pos, n := 0, 0
	if shortLen {
		if len(b) < 2 {
			return "", 0
		}
		n, pos = int(b[0]), 1
	} else {
		if len(b) < 3 {
			return "", 0
		}
		n, pos = int(binary.LittleEndian.Uint16(b)), 2
	}
	flags := b[pos]
	pos++
	runs, ext := 0, 0
	if flags&0x08 != 0 && pos+2 <= len(b) {
		runs, pos = int(binary.LittleEndian.Uint16(b[pos:])), pos+2
	}
	if flags&0x04 != 0 && pos+4 <= len(b) {
		ext, pos = int(binary.LittleEndian.Uint32(b[pos:])), pos+4
	}
	s, used := decodeChars(b[pos:], n, flags&1 != 0)
	return s, pos + used + runs*4 + ext
}

func decodeChars(b []byte, n int, wide bool) (string, int) {
	if wide {
		if n*2 > len(b) {
			n = len(b) / 2
		}
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[2*i:])
		}
		return string(utf16.Decode(u)), n * 2
	}
	if n > len(b) {
		n = len(b)
	}
	r := make([]rune, n)
	for i := 0; i < n; i++ {
		r[i] = rune(b[i]) // compressed strings are Latin-1
	}
	return string(r), n
}

// readSST parses the shared string table; a string that runs into the next CONTINUE chunk
// restarts there with a fresh "wide" flag byte.
func readSST(chunks [][]byte) []string {
	if len(chunks[0]) < 8 {
		return nil
	}
	total := int(binary.LittleEndian.Uint32(chunks[0][4:]))
	ci, pos := 0, 8
	need := func(n int) bool { // n bytes available in the current chunk, hopping to the next if it is used up
		for pos+n > len(chunks[ci]) {
			if pos < len(chunks[ci]) || ci+1 >= len(chunks) {
				return false
			}
			ci, pos = ci+1, 0
		}
		return true
	}
	skip := func(n int) {
		for n > 0 {
			if pos >= len(chunks[ci]) {
				if ci+1 >= len(chunks) {
					return
				}
				ci, pos = ci+1, 0
				continue
			}
			k := min(n, len(chunks[ci])-pos)
			pos += k
			n -= k
		}
	}
	var out []string
	for len(out) < total {
		if !need(3) {
			break
		}
		n := int(binary.LittleEndian.Uint16(chunks[ci][pos:]))
		flags := chunks[ci][pos+2]
		pos += 3
		runs, ext := 0, 0
		if flags&0x08 != 0 {
			if !need(2) {
				break
			}
			runs, pos = int(binary.LittleEndian.Uint16(chunks[ci][pos:])), pos+2
		}
		if flags&0x04 != 0 {
			if !need(4) {
				break
			}
			ext, pos = int(binary.LittleEndian.Uint32(chunks[ci][pos:])), pos+4
		}
		wide := flags&1 != 0
		var sb strings.Builder
		for left := n; left > 0; {
			if pos >= len(chunks[ci]) {
				if ci+1 >= len(chunks) {
					return out
				}
				ci++
				wide = len(chunks[ci]) > 0 && chunks[ci][0]&1 != 0
				pos = 1
				continue
			}
			unit := 1
			if wide {
				unit = 2
			}
			k := min(left, (len(chunks[ci])-pos)/unit)
			if k == 0 {
				pos = len(chunks[ci]) // dangling odd byte
				continue
			}
			s, used := decodeChars(chunks[ci][pos:], k, wide)
			sb.WriteString(s)
			pos += used
			left -= k
		}
		skip(runs*4 + ext)
		out = append(out, sb.String())
	}
	return out
}
