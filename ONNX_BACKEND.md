# ONNX page-parser backend

An opt-in, CPU-only path for pages that have no text layer (scans, photos, image PDFs). A layout
model finds the blocks of a page, a light OCR (PaddleOCR via ONNX Runtime) reads them, and the
result is stitched into Markdown in reading order. It is an alternative to sending every such page to
the Granite-Docling VLM, which costs 10–17 s per page on CPU.

This document covers how to use it, how well it works (measured), what it cannot do, and what has
**not** been evaluated.

## Status

| Part | State |
| --- | --- |
| Layout detection (PicoDet / PubLayNet), OCR (PP-OCRv5 latin, PP-OCRv4 chinese), reading order, text/list/title/table assembly | Implemented and measured on x86-64 |
| OTSL → Markdown/HTML table state machine (`lcel`, `ucel`, `xcel`, `ched`, `rhed`, `srow`, `ecel`, `fcel`, `nl`) | Implemented, unit-tested |
| Model download with SHA-256 check, `make download-doc-models` | Implemented, tested against the real hosts |
| Models linked into the binary (`-tags embedmodels`) | Implemented, verified with an empty model directory |
| Deep path: tables sent to Granite-Docling through eif-runtime | **Wired but not evaluated** (see below) |
| ARM64 | **Not tested** (only x86-64 was available) |

The backend is **off by default**. Nothing changes for existing users until `GLEANN_DOCLING_ONNX=1` is set.

## Using it

Requirements: `libonnxruntime` (tested with 1.26.0; looked up in `$GLEANN_ORT_LIB`, next to the
executable, `~/.gleann/lib`, then the system library directories) and poppler (`pdftoppm`, already
required by the plugin).

```bash
make download-doc-models            # ~20 MB into ~/.gleann/models/docparser (PROFILE=latin|chinese|all)
GLEANN_DOCLING_ONNX=1 ./bin/gleann-plugin-docs convert scan.pdf
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `GLEANN_DOCLING_ONNX` | off | Enable the ONNX page parser for scanned pages and images |
| `GLEANN_DOCLING_OCR_PROFILE` | `latin` | `latin` (PP-OCRv5, Turkish and 30 other Latin-script languages) or `chinese` (PP-OCRv4, Chinese + English) |
| `GLEANN_DOCLING_DPI` | `150` | Resolution PDF pages are rasterised at |
| `GLEANN_DOCLING_ONNX_DEEP` | off | Send table blocks to the VLM (needs `GLEANN_DOCLING_VLM=1`) |
| `GLEANN_DOCPARSER_MODELS` | `~/.gleann/models/docparser` | Model directory |
| `GLEANN_DOCPARSER_OFFLINE` | off | Never download missing models (by default they are fetched on first use) |
| `GLEANN_ORT_LIB` | auto | Path of the ONNX Runtime library |

`make build-embedded` produces `bin/gleann-plugin-docs-embedded` with the models linked in (about
+20 MB, 12 MB → 32 MB); it needs no download, only `libonnxruntime`. The default binary stays small.

`gleann-plugin-docs models list|verify|download [--profile latin|chinese|all] [--dir DIR]` manages the files.

## How it works

```
page image ─► layout model ─► blocks: text · title · list · table · figure
                  │
                  ├─ text / title / list ─► DB text detector ─► line crops ─► CTC recogniser ─► Markdown
                  ├─ table ───────────────► OCR lines rebuilt into a grid ─► Markdown table
                  │                         (or the VLM when the deep path is on)
                  ├─ figure ──────────────► skipped
                  └─ text the layout model missed ─► page-wide detection ─► read as extra blocks
blocks ─► XY-cut reading order ─► Markdown
```

Three behaviours exist because measurements showed the layout model alone loses text:

- **Coverage safety net.** The layout model was trained on PubLayNet, which has no reference lists,
  running headers or side notes. The first test page lost its entire reference list (word recall 19 %).
  The parser now also detects text lines on the whole page and reads those no block covers. A page-sized
  "figure" (a brochure's background photo) does not count as covering the text on it.
- **Headers and footers are dropped.** Recovered lines in the top and bottom 8 % of the page (running
  titles, page numbers, download notices) are index noise and are removed.
- **Column-aware reading order.** A recursive XY-cut reads a two-column page column by column, with
  full-width titles and tables separating bands. Sorting by Y alone interleaved the columns.

## Models

All files are downloaded with `gleann-plugin-docs models download` (pinned revision, SHA-256 checked,
Apache-2.0) into `~/.gleann/models/docparser`, or built into the binary with `-tags embedmodels`.

| Role | File | Size | Source |
| --- | --- | --- | --- |
| Layout (text, title, list, table, figure) | `layout_publaynet.onnx` (PicoDet, PubLayNet) | 7.4 MB | Desperado-JT/RapidLayout-PP-Structure |
| Text detection, default | `ppocrv5_mobile_det.onnx` (PP-OCRv5 mobile DB) | 4.8 MB | PaddlePaddle/PP-OCRv5_mobile_det_onnx |
| Text recognition, default (`latin`, Turkish included) | `ppocrv5_latin_rec.onnx` + `ppocrv5_latin_dict.txt` | 7.9 MB + 1.6 KB | monkt/paddleocr-onnx |
| Text detection, `chinese` profile | `ch_PP-OCRv4_det_infer.onnx` | 4.7 MB | SWHL/RapidOCR |
| Text recognition, `chinese` profile (no `ç ğ ı İ ö ş`) | `ch_PP-OCRv4_rec_infer.onnx` | 10.9 MB | SWHL/RapidOCR |

The default set is about 20 MB. The deep path additionally needs a Granite-Docling model for
eif-runtime, which is not part of this download (see the last section).

## Measured results

Hardware: Intel Core i9-13900H (14 cores / 20 threads), 62 GB RAM, ONNX Runtime 1.26.0 CPU, `latin`
profile, 150 dpi, default settings (4 workers). The reference is each PDF's own text layer, so the page
image is the only input the parser sees (`make bench-docparser PDF=... PAGES=...`).

*Word recall* = share of reference words found (order-insensitive); *precision* = share of output words
that are correct; *CER* = character error rate (order-sensitive, so it is inflated by reading-order and
formula differences); *recall\** = recall after folding Turkish letters to ASCII.

| Document | Pages | Latency / page | Recall | Precision | CER | Recall\* |
| --- | --- | --- | --- | --- | --- | --- |
| English journal article, two columns | 4 | 0.9 s | 94.6 % | 96.5 % | 8.0 % | 94.6 % |
| English business report | 3 | 0.85 s | 95.9 % | 96.0 % | 1.8 % | 95.9 % |
| CV (mixed Turkish/English) | 2 | 0.8 s | 91.4 % | 94.8 % | 6.0 % | 91.8 % |
| Turkish forms with tables | 3 | 0.55 s | 79.5 % | 82.9 % | 32.1 % | 88.1 % |
| Turkish paper with equations | 4 | 1.1 s | 70.7 % | 73.8 % | 26.7 % | 78.6 % |
| Product brochure (coloured backgrounds) | 2 | 0.5 s | 60.9 % | 65.0 % | 33.3 % | 67.4 % |
| Sparse document (about 46 words on page 1) | 3 | 0.5 s | 54.0 % | 62.2 % | 50.5 % | 65.0 % |

Peak memory is 520–630 MB. Loading the models takes about 0.3 s once per process, so the first page of
a `convert` call costs that much extra; the `--serve` daemon pays it at start-up. Text-heavy pages are
read well; the weak rows are pages whose content is mostly not running text (equations, diagrams,
decorative layouts). Everything here was measured on x86-64.

### Speed

The first version took about 2 s for a dense page and was judged too slow. Profiling showed where the
time went; one change helped a lot, one backfired:

| Change | Effect on a dense page (124 lines) |
| --- | --- |
| Turn off ONNX Runtime thread spinning (`session.intra_op.allow_spinning=0`) | 2.07 s → 1.78 s wall and **31 s → 15 s of CPU**. The default spin-waiting burned twice the CPU on a busy machine. |
| Detect text once per page instead of once per block, and run it alongside layout analysis | removes about 1.1 s of CPU per page of repeated block-level detection; recall changed by −0.6 to +1.6 points per document |
| Recognise every line of the page in one worker pool | no nested parallelism, workers stay busy |
| Batch the recognition (8 lines per call) | **slower**: 2.0 s → 3.4 s. The recogniser's attention layers make padding expensive. The batch size is fixed at 1 (`recBatch`, measurements in its comment). |
| int8 recogniser (onnxruntime quantisation, tried offline, not shipped) | Dynamic quantisation (MatMul only, or all ops): no speed-up, the model is Conv-dominated and CPU ConvInteger is slow. Static QDQ int8 (per-channel, 234 calibration crops): recognition CPU 3.5 s → 2.8-3.1 s (about 15 %) but word recall on the CV 92.9 % → 89.9 %. Not worth the accuracy; there is also no verified hosted int8 Latin model. |

Together the first three changes made pages 30–45 % faster (mean 1.49 → 0.90 s on the article pages,
1.82 → 1.09 s on the Turkish paper).

Dense page, wall time by CPU budget: 1 core, 1 thread **6.7 s**; 4 cores (2 workers × 2 threads) **3.4 s**;
all 20 threads (4 workers × 5 threads, the default) **1.36 s**. About 52 ms of CPU per line, almost all of
it in the recogniser, so cost scales with the amount of text; a sparse page takes 0.5 s. More workers than
the default do not help (8 workers: 1.7 s, 20 workers: 2.4 s, with twice the CPU time).

**Where it is still slow.** On a machine with a few weak cores (an embedded board, a container with one
CPU) a text-dense page takes several seconds, because the recogniser is compute-bound. Options that were
not done: an int8-quantised recogniser (no verified artifact to download, and the accuracy cost is
unknown), or a lower detector resolution (not measured).

**OCR profile.** `latin` beat `chinese` on every document (measured before the speed work, at 200 dpi):

| Document | `chinese` recall | `latin` recall |
| --- | --- | --- |
| English article | 89.6 % | 95.2 % |
| English report | 94.8 % | 96.2 % |
| CV | 82.5 % | 90.8 % |
| Turkish paper | 54.5 % | 70.9 % |

The PP-OCRv4 dictionary has no `ç ğ ı İ ö ş` (only `ü`), so the `chinese` profile cannot spell Turkish;
`latin` is the default for that reason. Keep `chinese` only for Chinese text.

**Resolution.** With the `latin` profile the render resolution hardly matters, so the default is 150 dpi
(the same the VLM path uses). English article pages: 150 dpi recall 95.2 %, 200 dpi 95.2 %, 300 dpi 95.3 %
(1.5 s, 1.7 s, 1.9 s per page before the speed work); Turkish paper: 70.8 %, 70.9 %, 70.0 %. Only these two
documents were measured at several resolutions. (The older `chinese` recogniser did gain from resolution,
86.7 % → 92.0 % from 150 to 300 dpi, one more reason to prefer `latin`.)

## Known limitations

- **Turkish ı / İ.** The recogniser often confuses dotless `ı` with `i` and `İ` with `I`
  (`yaşıyoruz` → `yaşiyoruz`, `İstanbul` → `Istanbul`). Folding these letters is worth about 9 points of
  word recall on the Turkish paper (70.9 % → 79.7 %). Other Turkish letters (`ç ğ ö ş ü`) are read
  reliably. Search that must match exactly should normalise these letters; the integration test records
  the behaviour and does not assert it.
- **Equations are not read.** There is no formula class in the layout model; equations come out as
  scrambled text or nothing.
- **Tables are rebuilt from OCR boxes**, which works for regular ruled tables (a synthetic table was
  reproduced exactly) but has no notion of merged cells, and only runs on tables the layout model
  detects. A wide specification table in the brochure came out as flowing text, not as a table. The OTSL parser
  handles merged cells, but only the VLM produces OTSL.
- **The layout model is trained on PubLayNet** (scientific articles). Magazine and brochure layouts
  fall back mostly on the coverage safety net.
- **Figures are skipped**, so text inside charts and diagrams is lost unless the deep path handles it.
- **Low-contrast and coloured text** (light-blue hyperlinks) is read less reliably; vertical and
  rotated text is not handled; there is no handwriting support.
- **Reading order** is validated on single- and two-column documents only.
- **Not CGo-free.** `onnxruntime_go` loads the runtime with `dlopen` through CGo. The plugin already
  needs CGo for eif-runtime, but "single static binary" holds only if `libonnxruntime` is shipped beside it.

## Not evaluated: Granite-Docling through eif-runtime

As requested, this backend was judged without the Granite-Docling path. What exists:

- With `GLEANN_DOCLING_ONNX_DEEP=1` and `GLEANN_DOCLING_VLM=1`, table blocks are sent to the loaded
  Granite model with the box-guided prompt `Convert table at <loc_x1><loc_y1><loc_x2><loc_y2> to OTSL.`
  (coordinates on the 0..500 grid), and the OTSL reply is rendered by the new state machine. If the model
  fails or answers nothing, the OCR grid is used.
- **Nothing measured:** no latency, no accuracy, and it is **unknown whether the BitNet `.eifm` model
  understands box-guided prompts** (the plugin has only ever used "Convert this page to docling."). Only
  the prompt construction is unit-tested.
- The spec's idea of downloading a Granite-Docling INT8 ONNX/GGUF on first run was **not** done: the
  engine loads an `.eifm` file produced by the eif-runtime tooling, and no verified download URL and
  checksum exist for it. Downloading an unverified artifact would defeat the SHA-256 check.
- A formula class is also routed to the deep path in code, but the layout model never produces one.

### Preliminary trial of three Granite-Docling variants

A single run on one page (Turkish form with a table, 150 dpi, x86-64, 14 threads, greedy decoding), to
see whether a smaller quantisation is usable. One page, one run: an impression, not a benchmark.
Wall times include model loading and cover different numbers of generated tokens.

| Variant | Size | Wall time | Result |
| --- | --- | --- | --- |
| BitNet 1.58-bit `.eifm` (`tevfikk/granite-docling-258M-bitnet`, eif-runtime) | 518 MB | 18.5 s | Garbled labels and broken structure |
| Q4_K_M GGUF (`infil00p/granite-docling-258M-GGUF`, llama.cpp) | 139 MB + 190 MB `mmproj` | 19.3 s | Labels read, the value column missing, repeated logo blocks, ends in a repetition loop |
| Q8_0 GGUF (same repo, llama.cpp) | 178 MB + 190 MB `mmproj` | 28.0 s | Labels and values correct, clean OTSL; ends in a repetition loop |

- The vision encoder dominates the BitNet run: 15 s of 18.5 s for 13 image tiles. Quantising the language
  model does not change that; fewer tiles (a cropped table region) would.
- The repetition loops are a decoding setting (no repetition penalty, 1024-token limit), not a quantisation result.
- These GGUF files cannot be loaded by eif-runtime's vision path (it loads `.eifm`); llama.cpp's
  `llama-mtmd-cli` with `--chat-template` from the repo was used.

## Deviations from the original brief

| Brief | What was built | Why |
| --- | --- | --- |
| `pkg/docparser` in gleann | `internal/docparser` in this plugin | The plugin is where document parsing lives |
| RapidLayout `PP-DocLayout` | `layout_publaynet.onnx` (PicoDet, 7.4 MB) | A small ONNX layout model with a stable licence that exports without extra tooling; classes are text/title/list/table/figure |
| PP-OCRv5/v6 mobile | PP-OCRv5 latin (+ v5 mobile detector); PP-OCRv4 chinese as an option | The Chinese/English dictionary cannot spell Turkish |
| Recognition model only | Detector **and** recogniser | A layout block holds several lines; the recogniser reads one |
| `go:embed` of the light models | `go:embed` behind `-tags embedmodels` | Keeps 20 MB of models out of git and the default binary |
| `go-fitz` / `pdfcpu` page rendering | `pdftoppm` (already a dependency) | No new dependency |
| Pure Go Markdown from OTSL | New state machine in `internal/parser/otsl.go` | The existing converter dropped merged cells, shifting columns |

Image handling uses only the standard library and `golang.org/x/image/draw`. No Python, no OpenCV. All
models run in float32.

## Verification performed

- `go test ./...` passes (95 tests, 1 skipped: the live Granite inference test, no model on this machine);
  `go test -race ./internal/docparser` passes.
- Unit tests, no models needed: tensor preprocessing, PicoDet decoding and NMS, DB post-processing, CTC
  decoding, reading order, coverage/recovery, de-duplication, list/table assembly, metrics, OTSL spans, the
  downloader (mock server: success, skip when valid, replace corrupt, checksum mismatch installs nothing,
  HTTP error, cancellation).
- Integration tests (skipped when models or `libonnxruntime` are missing): a synthetic page with a title,
  paragraphs, Turkish text, a list and a ruled table; cancellation; identical output across concurrent
  runs; a blank page; a scanned PDF built in the test; the engine path for images and scanned PDFs; the
  error message when OCR is not enabled.
- Real downloads from the pinned model URLs, checksum-verified; an embedded-models binary run with an empty
  model directory.

## Next steps, in order of value

1. Run the benchmark on **ARM64** (the models are architecture-independent, but `libonnxruntime` for
   aarch64 and the `dlopen` path were not exercised).
2. Decide whether the backend should be on by default once ARM64 numbers exist.
3. Evaluate the Granite deep path separately, starting with whether the `.eifm` model accepts box-guided prompts.
4. Replace the OCR-grid table fallback with a table-structure model, and add a layout model that knows
   references, headers and formulas (PP-DocLayout-S/L ONNX).
5. Normalise `ı/İ` for Turkish search, or add a dictionary-based correction pass.
