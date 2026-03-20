package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const defaultPrompt = "Extract all text from this document"

var version = "dev"

// ---------------------------------------------------------------------------
// API wire types
// ---------------------------------------------------------------------------

type ImageURL struct {
	URL string `json:"url"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	Text     string    `json:"text,omitempty"`
}

type Message struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type Choice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// GLM-OCR structured response
//
// choices[0].message.content is a JSON string:
//   [[{"index":0,"label":"text","content":"…","bbox_2d":null}, …], …]
// Outer array = pages, inner = blocks per page.
// ---------------------------------------------------------------------------

type OCRBlock struct {
	Index   int         `json:"index"`
	Label   string      `json:"label"`
	Content string      `json:"content"`
	BBox2D  interface{} `json:"bbox_2d"`
}

func parseOCRContent(raw string, pageCount int) (pages [][]OCRBlock, structured bool) {
	raw = strings.TrimSpace(raw)
	
	// Try to find a JSON array in the string (it might be surrounded by text or markdown)
	start := strings.Index(raw, "[[")
	end := strings.LastIndex(raw, "]]")
	if start != -1 && end != -1 && end > start {
		jsonPart := raw[start : end+2]
		if err := json.Unmarshal([]byte(jsonPart), &pages); err == nil && len(pages) > 0 {
			return pages, true
		}
	}

	// If not structured, and we have multiple pages, try to split the plain text 
	// (this is a heuristic, usually the model returns structured data for multi-page)
	if pageCount > 1 {
		// If the model returned plain text for multiple images, it often puts them 
		// all together. We don't have a good way to split it back to pages perfectly 
		// without markers, so we just treat it as one large page 1 for now.
		return [][]OCRBlock{{{Index: 0, Label: "text", Content: raw}}}, false
	}
	
	return [][]OCRBlock{{{Index: 0, Label: "text", Content: raw}}}, false
}

// ---------------------------------------------------------------------------
// Output formatters
// ---------------------------------------------------------------------------

func renderMarkdown(pages [][]OCRBlock) string {
	var sb strings.Builder
	for pi, page := range pages {
		if len(pages) > 1 {
			fmt.Fprintf(&sb, "\n---\n<!-- page %d -->\n\n", pi+1)
		}
		for _, b := range page {
			content := strings.TrimSpace(b.Content)
			if content == "" {
				continue
			}
			switch strings.ToLower(b.Label) {
			case "title":
				fmt.Fprintf(&sb, "## %s\n\n", content)
			case "figure", "caption":
				fmt.Fprintf(&sb, "*%s*\n\n", content)
			default:
				sb.WriteString(content)
				sb.WriteString("\n\n")
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func tableRowToPlain(line string) string {
	if strings.Contains(line, "---") {
		return ""
	}
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			cells = append(cells, p)
		}
	}
	return strings.Join(cells, "  ")
}

func renderPlainText(pages [][]OCRBlock) string {
	md := renderMarkdown(pages)
	replacer := strings.NewReplacer(
		"**", "", "__", "", "```", "", "`", "", "*", "", "_", "",
	)
	var out []string
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "<!--") {
			continue
		}
		stripped := strings.TrimLeft(line, "#")
		if len(stripped) != len(line) {
			stripped = strings.TrimSpace(stripped)
		}
		if strings.Contains(stripped, "|") {
			stripped = tableRowToPlain(stripped)
		}
		out = append(out, replacer.Replace(stripped))
	}
	result := strings.Join(out, "\n")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

type JSONOutputBlock struct {
	Page    int         `json:"page"`
	Index   int         `json:"index"`
	Label   string      `json:"label"`
	Content string      `json:"content"`
	BBox2D  interface{} `json:"bbox_2d"`
}

type JSONOutput struct {
	Source string            `json:"source"`
	Model  string            `json:"model"`
	Pages  int               `json:"pages"`
	Blocks []JSONOutputBlock `json:"blocks"`
}

func renderJSON(pages [][]OCRBlock, source, model string) (string, error) {
	out := JSONOutput{
		Source: source,
		Model:  model,
		Pages:  len(pages),
		Blocks: make([]JSONOutputBlock, 0),
	}
	for pi, page := range pages {
		for _, b := range page {
			out.Blocks = append(out.Blocks, JSONOutputBlock{
				Page:    pi + 1,
				Index:   b.Index,
				Label:   b.Label,
				Content: b.Content,
				BBox2D:  b.BBox2D,
			})
		}
	}
	bs, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

// ---------------------------------------------------------------------------
// URI helpers
// ---------------------------------------------------------------------------

func mimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// toFileURI returns an absolute file:// URI.
// Used when the server runs locally and can read the filesystem directly.
func toFileURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}
	return "file://" + filepath.ToSlash(abs), nil
}

// toDataURI base64-encodes the file and returns a data: URI.
// Used when the server is remote and can't access the local filesystem.
func toDataURI(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}
	return fmt.Sprintf("data:%s;base64,%s",
		mimeType(path), base64.StdEncoding.EncodeToString(data)), nil
}

// ---------------------------------------------------------------------------
// HTTP call
// ---------------------------------------------------------------------------

func callAPI(apiURL, model, promptText string, imageURIs []string) (*ChatResponse, error) {
	var content []ContentPart
	for _, uri := range imageURIs {
		content = append(content, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: uri},
		})
	}
	content = append(content, ContentPart{
		Type: "text",
		Text: promptText,
	})

	body, err := json.Marshal(ChatRequest{
		Model: model,
		Messages: []Message{{
			Role:    "user",
			Content: content,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server %d: %s", resp.StatusCode, string(respBody))
	}

	var cr ChatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return nil, fmt.Errorf("parsing response: %w\nraw: %s", err, string(respBody))
	}
	return &cr, nil
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func run(args []string) error {
	fs := flag.NewFlagSet("ocr", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	endpoint   := fs.String("endpoint", "http://localhost:8080", "API base URL")
	port       := fs.Int("port", 0, "Override port in --endpoint")
	model      := fs.String("model", "zai-org/GLM-OCR", "Model name")
	prompt     := fs.String("prompt", defaultPrompt, "Instruction sent with the file")
	outputFile := fs.String("output", "", "Write output to file instead of stdout")
	fmtMD      := fs.Bool("markdown", false, "Output as Markdown (default)")
	fmtText    := fs.Bool("text", false, "Output as plain text")
	fmtJSON    := fs.Bool("json", false, "Output as JSON")
	embed      := fs.Bool("embed", false, "Send file as base64 data URI (use for remote servers)")
	rawMode    := fs.Bool("raw", false, "Dump raw model response and exit (debug)")
	showVer    := fs.Bool("version", false, "Print version and exit")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "ocr %s\n\nUsage: ocr [options] <file>\n\nOptions:\n", version)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  ocr scan.png
  ocr -output result.md document.pdf
  ocr document.pdf -output result.md
  ocr --text --output result.txt invoice.pdf
  ocr --json --output result.json photo.jpg
  ocr --embed --endpoint http://10.0.0.5 --port 9000 doc.pdf
  ocr --raw scan.png`)
	}

	// Separate flags and the positional file argument --------------------------
	var flags []string
	var files []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			// If it's a flag that takes a value (like -output), grab the next arg too
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				// These specific flags take values:
				valFlags := []string{"-endpoint", "--endpoint", "-port", "--port", "-model", "--model", "-prompt", "--prompt", "-output", "--output"}
				isValFlag := false
				for _, vf := range valFlags {
					if arg == vf {
						isValFlag = true
						break
					}
				}
				if isValFlag {
					flags = append(flags, args[i+1])
					i++
				}
			}
		} else {
			files = append(files, arg)
		}
	}

	if err := fs.Parse(flags); err != nil {
		return err
	}

	if *showVer {
		fmt.Printf("ocr %s\n", version)
		return nil
	}
	if len(files) < 1 {
		fs.Usage()
		return fmt.Errorf("no input file specified")
	}

	inputFile := files[0]
	if _, err := os.Stat(inputFile); err != nil {
		return fmt.Errorf("cannot access %q: %w", inputFile, err)
	}

	// Build endpoint URL -------------------------------------------------------
	base := strings.TrimRight(*endpoint, "/")
	if *port != 0 {
		if idx := strings.LastIndex(base, ":"); idx > strings.Index(base, "//") {
			base = base[:idx]
		}
		base = fmt.Sprintf("%s:%d", base, *port)
	}
	apiURL := base + "/v1/chat/completions"

	// Build image URIs ---------------------------------------------------------
	var imageURIs []string
	isPDF := strings.ToLower(filepath.Ext(inputFile)) == ".pdf"

	if isPDF {
		fmt.Fprintf(os.Stderr, "→ rendering PDF to images...\n")
		var err error
		imageURIs, err = renderPDFToDataURIs(inputFile)
		if err != nil {
			return fmt.Errorf("rendering PDF: %w", err)
		}
	} else {
		var uri string
		var err error
		if *embed {
			uri, err = toDataURI(inputFile)
		} else {
			uri, err = toFileURI(inputFile)
		}
		if err != nil {
			return err
		}
		imageURIs = []string{uri}
	}

	// Call API -----------------------------------------------------------------
	fmt.Fprintf(os.Stderr, "→ POST %s  [model: %s] [%d image(s)]\n", apiURL, *model, len(imageURIs))

	cr, err := callAPI(apiURL, *model, *prompt, imageURIs)

	if err != nil {
		return err
	}
	if cr.Error != nil {
		return fmt.Errorf("API error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}

	content := cr.Choices[0].Message.Content

	if *rawMode {
		fmt.Println(content)
		return nil
	}

	// Parse & render -----------------------------------------------------------
	pages, structured := parseOCRContent(content, len(imageURIs))
	if !structured {
		fmt.Fprintln(os.Stderr, "⚠ response is not structured JSON — rendered as plain content")
	}

	var result string
	switch {
	case *fmtJSON:
		result, err = renderJSON(pages, inputFile, *model)
		if err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	case *fmtText:
		result = renderPlainText(pages)
	default:
		_ = *fmtMD
		result = renderMarkdown(pages)
	}

	// Write output -------------------------------------------------------------
	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, []byte(result+"\n"), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", *outputFile, err)
		}
		fmt.Fprintf(os.Stderr, "✓ written to %s\n", *outputFile)
	} else {
		fmt.Println(result)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
