# ocr-cli

A lightweight CLI that sends images or PDFs to a GLM-OCR (or any OpenAI-compatible)
chat-completions endpoint and renders the result as **Markdown**, **plain text**, or **JSON**.

## Dependencies

One pure-Go module, no CGo, no system tools required:

| Module | Purpose |
|---|---|
| `github.com/pdfcpu/pdfcpu` | Split multi-page PDFs into single-page files before sending |

## Build

```bash
# Fetch dependency and build for the current platform
go mod tidy
go build -o ocr .

# Cross-compile for all platforms (output → ./dist/)
make all
```

Supported targets: `linux/amd64`, `linux/arm64`, `linux/arm`,
`darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`.

## Usage

```
ocr [options] <file>

Options:
  -endpoint string   API base URL                          (default "http://localhost:8080")
  -port     int      Override port in --endpoint
  -model    string   Model name                            (default "zai-org/GLM-OCR")
  -prompt   string   Instruction sent with the file        (default "Extract all text from this document")
  -output   string   Write output to file instead of stdout
  -markdown          Output as Markdown (default)
  -text              Output as plain text (strips MD decoration, flattens tables)
  -json              Output as JSON  {source, model, pages, blocks:[]}
  -embed             Send files as base64 data-URIs instead of file:// URIs
  -raw               Dump raw model content string and exit (debug)
  -version           Print version and exit
```

## Examples

```bash
# Basic — prints Markdown to stdout
ocr scan.png

# Multi-page PDF — automatically split into pages before sending
ocr document.pdf

# Plain text to a file
ocr --text --output result.txt invoice.pdf

# Structured JSON output
ocr --json --output result.json photo.jpg

# Remote server — use --embed to send file bytes instead of file:// URIs
ocr --embed --endpoint http://10.0.0.5 --port 9000 doc.pdf

# Custom prompt
ocr --prompt "List every line item with its amount" receipt.png

# Debug: see exactly what the model returned
ocr --raw scan.png
```

## How PDF input works

The GLM-OCR API only accepts images, not raw PDF blobs.
When a PDF is given, the CLI:

1. Uses **pdfcpu** (pure Go) to count pages and split the PDF into
   individual single-page `.pdf` files in a temporary directory.
2. Sends every page as a separate `image_url` content part in one request.
3. Cleans up the temporary files automatically when the process exits.

For a single-page PDF the split step is skipped entirely and the original
file is sent directly.

By default pages are sent as `file://` URIs (fast, zero copy — requires
the server to run on the same machine with access to the same filesystem).
Pass `--embed` to base64-encode each page instead (required for remote servers).

## Output formats

### Markdown (default)

Block labels from the model are mapped to Markdown elements:

| Label | Rendered as |
|---|---|
| `title` | `## heading` |
| `figure` / `caption` | *italic* |
| `table` | verbatim (model already outputs MD tables) |
| `text` / other | paragraph |

Multi-page documents get `---` page separators.

### Plain text (`--text`)

Renders Markdown first, then strips all decoration and flattens
`| col | col |` table rows to `col  col`.

### JSON (`--json`)

```json
{
  "source": "invoice.pdf",
  "model": "zai-org/GLM-OCR",
  "pages": 2,
  "blocks": [
    { "page": 1, "index": 0, "label": "title",  "content": "…", "bbox_2d": null },
    { "page": 1, "index": 1, "label": "text",   "content": "…", "bbox_2d": null },
    { "page": 2, "index": 0, "label": "table",  "content": "…", "bbox_2d": [x,y,w,h] }
  ]
}
```

`bbox_2d` is passed through as-is from the model (null or a coordinate array).

## Notes

- Progress and warnings are written to **stderr** so stdout can be safely
  piped or redirected.
- If the model returns plain text instead of the structured `[[{…}]]` JSON,
  a warning is printed and the raw text is rendered through the chosen formatter.
- Format flags (`--markdown`, `--text`, `--json`) and `--output` are fully
  orthogonal — any combination works.
