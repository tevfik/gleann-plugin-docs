# Changelog

## [1.5.5]

### Security

- Access control: every route requires a loopback `Host` (403 otherwise, DNS rebinding), `/health` included;
  every route except `/health` requires the bearer token when one is configured (`GLEANN_PLUGIN_TOKEN` from
  the gleann host, or `--token` / `GLEANN_DOCS_TOKEN`).
- Zip documents (docx, xlsx, pptx, epub, odt, ods, odp) are checked before they are read: entry count, size per
  member, total size and expansion ratio (413).
- A slide, worksheet or spine item that is referenced several times is read and printed once; xls sheet
  records that point at the same sheet are rendered once. All zip data read during one conversion
  (documents and embedded images) is charged against one budget equal to the zip total cap
  (`GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES`, default 1 GiB).
- Total Markdown output per conversion is capped at `GLEANN_DOCS_MAX_MARKDOWN_BYTES` (default 64 MiB), and
  intermediate output buffers (slides, cells, list items, blockquotes, links, table cells, sheets, embedded
  picture text, PDF page text including OCR/model and table output) share that one budget instead of each
  having its own. A write past it answers 413. A test scans the engine sources and fails when output text is
  built with a bare buffer, builder or join outside the budgeted builder (each exemption is listed with its
  reason). Not covered by that budget: the parsed input (XML/HTML tree, cells read so far, the OLE2 stream of
  an xls), the PDF tools' own stdout and per-page layout work (bounded by page geometry), and brief copies
  of an extractor's result by its caller, so peak memory can reach about twice the cap.
- Tables (docx, xlsx and xls, html and epub, pptx) are written by one shared renderer. Before it renders
  anything it projects the output size (rows x width x the minimum bytes per cell, the separator row and the
  cell text) and answers 413 when that exceeds what is left of the Markdown cap; it writes rows straight into
  the size-capped output and never builds the padded grid. A table wider than 16,384 columns (Excel's XFD) is
  422. A 2 KB docx with a 30,000-cell row is refused instead of exhausting memory.
- xlsx: a cell column past XFD (16,384) or a row past 1,048,576 is 422, and the padding cells of a sparse row
  are charged against the Markdown cap, so a few bytes of XML cannot make the reader pad every row to a
  huge column.
- Embedded pictures of docx and pptx files are decoded, read by OCR or the model and released one at a time
  (memory is one decoded picture, not up to 40), and their reads count against the zip budget.
- The anydoc CLI output is read with a limit at the Markdown cap.
- The pixel budget check is overflow-safe (a TIFF declaring 4294967295x4294967295 is 413, a non-positive size is
  422) and a panic of an image decoder on a hostile file is 422 instead of a dropped connection.
- Temporary files live under a per-user root, `<temp dir>/gleann-plugin-docs-<uid>/<port>`. The root is refused
  (logged, system temp directory used, sweep skipped) when it is a symbolic link, belongs to another user or is
  writable by group or others. The sweep of leftovers from earlier versions never follows or removes symbolic
  links and only removes plain files and directories with this plugin's names that belong to the user.

### Fixed

- A conversion whose child process (pdftotext, pdftoppm, LibreOffice, anydoc) was killed because the request
  was cancelled or the server is shutting down is reported as cancelled (503, empty body), or 504 on a
  timeout, not as 422 or 500.
- The LibreOffice error is wrapped with `%w`: an exit status is 422 and the 180 s timeout reaches the 504
  mapping.
- `/health` no longer waits for a model run in progress.
- Shutdown lets running conversions finish for the stop grace, then cancels them and removes the temporary
  files.

### Added

- `.odt`, `.ods`, `.odp` and `.rtf` are advertised in `/info` and accepted by `/convert`. They are converted by
  anydoc: in process when the plugin is built with `-tags anydoc`, otherwise by the `anydoc` CLI (found on PATH,
  `~/.cargo/bin` or `~/.local/bin`); without it the answer is 503.
- Settings: `--token` flag and `GLEANN_DOCS_TOKEN` (bearer token clients must present; `GLEANN_PLUGIN_TOKEN` is
  set by the gleann host and accepted as well); `GLEANN_DOCS_MAX_PIXELS` (default 50,000,000),
  `GLEANN_DOCS_MAX_MARKDOWN_BYTES` (default 64 MiB), `GLEANN_DOCS_ZIP_MAX_ENTRIES` (10,000),
  `GLEANN_DOCS_ZIP_MAX_ENTRY_BYTES` (256 MiB), `GLEANN_DOCS_ZIP_MAX_TOTAL_BYTES` (1 GiB);
  `GLEANN_PLUGIN_STOP_GRACE` (Go duration, default 10 s) sets how long running conversions may finish after
  SIGINT/SIGTERM (grace minus one second, then they are cancelled). A value that is not a positive integer
  (duration for the grace) is ignored.
- `/health` reports which backends are usable and which formats are missing.
- CI workflow that builds, vets and tests (`go test -race`).
