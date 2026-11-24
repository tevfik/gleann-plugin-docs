package engine

import (
	"fmt"
	"io"
)

// maxTableCols is the widest table any extractor renders. It matches the last
// column Excel allows (XFD); a table wider than this is not a real document.
const maxTableCols = 16384

// tableOpts selects the small differences between the extractors' tables.
type tableOpts struct {
	// trimTrailing ignores empty cells at the end of a row when the table width
	// is computed (spreadsheets, whose rows are sparse).
	trimTrailing bool
	// escape is applied to every cell as it is written (nil: cells are written
	// as they are).
	escape func(string) string
	// trailingNewline ends the last row with a newline.
	trailingNewline bool
}

// remainingBudget is how many more bytes w may take: what is left under the
// Markdown cap for the size-capped builder, the whole cap for anything else.
func remainingBudget(w any) int64 {
	if m, ok := w.(*mdBuilder); ok {
		return m.ob.left
	}
	// A writer that is not drawn on a conversion budget must not exist: the
	// enforcement test (budget_test.go) rejects such buffers in the engine.
	return 0
}

// writeTable renders rows as a Markdown table (the first row is the header)
// into w, padding every row to the widest one. It is the only place the
// extractors pad a table.
//
// Nothing is allocated or written before the projected output size is known:
// rows x width x the minimum bytes per cell ("| " and " " around an empty
// cell), the separator row and the text of the cells. A projection above
// remaining is ErrTooLarge, a table wider than maxTableCols is ErrCorruptInput.
// Rows are then written one by one: the padded grid never exists in memory.
func writeTable(w io.StringWriter, rows [][]string, remaining int64, o tableOpts) error {
	width := 0
	var text int64
	for _, row := range rows {
		n := len(row)
		if o.trimTrailing {
			for n > 0 && row[n-1] == "" {
				n--
			}
		}
		if n > width {
			width = n
		}
		if width > maxTableCols {
			return fmt.Errorf("%w: a table is %d columns wide (limit %d)", ErrCorruptInput, width, maxTableCols)
		}
		for _, c := range row[:n] {
			text += int64(len(c))
		}
	}
	if width == 0 {
		return nil
	}
	const perCell = 3 // "| " or " | " plus the cell, at least
	w64 := int64(width)
	projected := int64(len(rows))*(perCell*w64+3) + (6*w64 + 3) + text
	if projected > remaining {
		return fmt.Errorf("%w: a %d x %d table needs at least %d bytes of Markdown, %d are left (GLEANN_DOCS_MAX_MARKDOWN_BYTES)",
			ErrTooLarge, len(rows), width, projected, remaining)
	}
	for i, row := range rows {
		if i > 0 {
			if _, err := w.WriteString("\n"); err != nil {
				return err
			}
		}
		if _, err := w.WriteString("|"); err != nil {
			return err
		}
		for c := 0; c < width; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
				if o.escape != nil {
					cell = o.escape(cell)
				}
			}
			if _, err := w.WriteString(" " + cell + " |"); err != nil {
				return err
			}
		}
		if i == 0 {
			if _, err := w.WriteString("\n|"); err != nil {
				return err
			}
			for c := 0; c < width; c++ {
				if _, err := w.WriteString(" --- |"); err != nil {
					return err
				}
			}
		}
	}
	if o.trailingNewline {
		_, err := w.WriteString("\n")
		return err
	}
	return nil
}
