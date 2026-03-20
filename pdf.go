package main

import (
	_ "embed"
	"fmt"
	"image/jpeg"
	"bytes"
	"encoding/base64"
	"os"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

//go:embed pdfium.wasm
var pdfiumWasm []byte

var pdfiumPool pdfium.Pool

func initPDFium() error {
	if pdfiumPool != nil {
		return nil
	}

	var err error
	pdfiumPool, err = webassembly.Init(webassembly.Config{
		MinIdle:  1,
		MaxIdle:  2,
		MaxTotal: 4,
		WASM:     pdfiumWasm,
	})
	if err != nil {
		return fmt.Errorf("initializing PDFium WASM pool: %w", err)
	}
	return nil
}

func renderPDFToDataURIs(path string) ([]string, error) {
	if err := initPDFium(); err != nil {
		return nil, err
	}

	instance, err := pdfiumPool.GetInstance(time.Minute)
	if err != nil {
		return nil, fmt.Errorf("getting PDFium instance: %w", err)
	}
	defer instance.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading PDF: %w", err)
	}

	doc, err := instance.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		return nil, fmt.Errorf("opening PDF document: %w", err)
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{
		Document: doc.Document,
	})

	pageCount, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	if err != nil {
		return nil, fmt.Errorf("getting page count: %w", err)
	}

	var dataURIs []string
	for i := 0; i < pageCount.PageCount; i++ {
		// Render page to image (300 DPI is standard for OCR)
		resp, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{
					Document: doc.Document,
					Index:    i,
				},
			},
			DPI: 300,
		})
		if err != nil {
			return nil, fmt.Errorf("rendering page %d: %w", i, err)
		}

		var buf bytes.Buffer
		// Encode as JPEG for smaller payload (OCR works fine with high-quality JPEG)
		if err := jpeg.Encode(&buf, resp.Result.Image, &jpeg.Options{Quality: 90}); err != nil {
			return nil, fmt.Errorf("encoding page %d to JPEG: %w", i, err)
		}

		dataURI := fmt.Sprintf("data:image/jpeg;base64,%s",
			base64.StdEncoding.EncodeToString(buf.Bytes()))
		dataURIs = append(dataURIs, dataURI)
	}

	return dataURIs, nil
}
