# 📄 GLM-OCR CLI

[![Go Report Card](https://goreportcard.com/badge/github.com/mamorett/glm-ocr)](https://goreportcard.com/report/github.com/mamorett/glm-ocr)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A lightweight, **self-contained** CLI that extracts structured text from images and multi-page PDFs using the **GLM-OCR** model.

> [!IMPORTANT]
> This tool is designed to work with any OpenAI-compatible chat-completions endpoint (like vLLM) hosting the `zai-org/GLM-OCR` model.

---

## ✨ Key Features

- 🚀 **Zero Dependencies**: Built with pure Go + WebAssembly. No need for `poppler`, `mupdf`, or any system-level PDF tools.
- 📦 **Self-Contained**: PDF rendering is embedded inside the binary. Single file, works everywhere.
- 📑 **Multi-Page PDF Support**: Automatically renders PDF pages to images and sends them in a single batch.
- 🎯 **Multiple Outputs**: Get results in **Markdown**, **Plain Text**, or **JSON**.
- 🌍 **Cross-Platform**: Compiled for Linux, macOS, and Windows (AMD64 & ARM64).

---

## 🛠️ Build & Install

Ensure you have **Go 1.25+** installed.

```bash
# Clone and build for your current platform
go build -o ocr .

# Cross-compile for all supported platforms
make all
```

The resulting binaries will be in the `dist/` folder.

---

## 📖 Usage

```bash
ocr [options] <file>
```

### Options

| Flag | Description | Default |
| :--- | :--- | :--- |
| `--endpoint` | API base URL | `http://localhost:8080` |
| `--model` | Model name | `zai-org/GLM-OCR` |
| `--prompt` | Instruction sent with the file | `Extract all text from this document` |
| `--output` | Write output to file instead of stdout | `stdout` |
| `--markdown` | Output as Markdown | `true` |
| `--text` | Output as plain text (flattens tables) | `false` |
| `--json` | Output as structured JSON | `false` |
| `--embed` | Send files as base64 data-URIs | `false` |
| `--raw` | Dump raw model response (debug) | `false` |

---

## 💡 Examples

### Basic OCR
Prints formatted Markdown to your terminal:
```bash
ocr scan.png
```

### Multi-page PDF
Renders all pages and combines them into a single Markdown document:
```bash
ocr document.pdf
```

### Remote Server
Use `--embed` if the vLLM server is on a different machine and cannot access your local filesystem:
```bash
ocr --embed --endpoint http://10.0.0.5 --port 8080 invoice.pdf
```

### Structured Data
Extract raw JSON data for programmatic use:
```bash
ocr --json --output result.json document.pdf
```

---

## ⚙️ How it Works

The **GLM-OCR** model requires images as input. Since it cannot process raw PDF blobs directly, this CLI performs the following steps:

1. **PDF Rendering**: Uses `go-pdfium` running on the `wazero` WebAssembly engine to render PDF pages into 300 DPI images.
2. **API Interaction**: All rendered images are sent as a sequence of `image_url` parts in a single `v1/chat/completions` request.
3. **Structured Parsing**: The model returns a JSON array of pages. The CLI parses this and renders it through the selected formatter.

---

## 📦 Output Formats

### 📝 Markdown (Default)
Maps block labels (title, text, table, figure) to appropriate Markdown elements. Multi-page documents are separated by `---` lines.

### 📄 Plain Text (`--text`)
Strips all Markdown decoration and flattens tables for easy copy-pasting or grep-ing.

### 🔢 JSON (`--json`)
Returns a full structured object containing the source path, model used, and a list of all detected blocks with their coordinates (`bbox_2d`).

---

## ⚖️ License
This project is licensed under the **MIT License**.
