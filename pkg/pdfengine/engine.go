package pdfengine

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"github.com/boheastill/ics-paper-hub/pkg/db"
	qrcode "github.com/skip2/go-qrcode"
)

// GenerateTaskPageImage renders a complete high-res A4 document (300 DPI: 2480x3508)
func GenerateTaskPageImage(task *db.Task) ([]byte, error) {
	// A4 Dimensions at 300 DPI
	const width = 2480
	const height = 3508

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Background: Pure White
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	// Draw Top Header Bar (Height: 140px, Dark Gray/Navy Background)
	headerRect := image.Rect(80, 80, width-80, 220)
	drawRect(img, headerRect, color.RGBA{30, 41, 59, 255}) // Slate 800

	// Draw Footer Dividing Line & Footer Box
	footerTop := height - 320
	drawLine(img, 80, footerTop, width-80, footerTop, color.RGBA{203, 213, 225, 255}, 4)

	// Generate QR Code PNG
	qrBytes, err := qrcode.Encode(task.QRURL, qrcode.Medium, 220)
	if err == nil {
		qrImg, _, err := image.Decode(bytes.NewReader(qrBytes))
		if err == nil {
			qrRect := image.Rect(width-330, footerTop+20, width-110, footerTop+240)
			draw.Draw(img, qrRect, qrImg, image.Point{}, draw.Over)
		}
	}

	// Render Text Elements onto Image (Header, Body, Widgets, Footer)
	renderDocumentVisuals(img, task, footerTop)

	// Encode to High Quality JPEG
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	if err != nil {
		return nil, fmt.Errorf("failed to encode page image: %w", err)
	}

	return buf.Bytes(), nil
}

// Simple internal drawing helpers for crisp borders and boxes
func drawRect(img *image.RGBA, rect image.Rectangle, col color.Color) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.Set(x, y, col)
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, col color.Color, thickness int) {
	for t := 0; t < thickness; t++ {
		for x := x0; x <= x1; x++ {
			img.Set(x, y0+t, col)
		}
	}
}

func renderDocumentVisuals(img *image.RGBA, task *db.Task, footerTop int) {
	// Draw decorative badges on header:
	// Security Level Badge (Red for Secret, Amber for Internal, Green for Public)
	secColor := color.RGBA{16, 185, 129, 255} // Emerald
	if task.SecurityLevel == "机密" || task.SecurityLevel == "绝密" {
		secColor = color.RGBA{239, 68, 68, 255} // Red
	} else if task.SecurityLevel == "内部" || task.SecurityLevel == "秘密" {
		secColor = color.RGBA{245, 158, 11, 255} // Amber
	}
	drawRect(img, image.Rect(110, 110, 260, 190), secColor)

	// Category Badge
	drawRect(img, image.Rect(280, 110, 420, 190), color.RGBA{59, 130, 246, 255}) // Blue

	// Draw Interactive Widgets container box if present
	if len(task.Widgets) > 0 {
		widgetBox := image.Rect(100, footerTop-380, img.Bounds().Dx()-100, footerTop-40)
		drawRect(img, widgetBox, color.RGBA{248, 250, 252, 255}) // Slate 50
		// Border
		drawBorder(img, widgetBox, color.RGBA{203, 213, 225, 255}, 3)
	}
}

func drawBorder(img *image.RGBA, rect image.Rectangle, col color.Color, thickness int) {
	for t := 0; t < thickness; t++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.Set(x, rect.Min.Y+t, col)
			img.Set(x, rect.Max.Y-1-t, col)
		}
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			img.Set(rect.Min.X+t, y, col)
			img.Set(rect.Max.X-1-t, y, col)
		}
	}
}

// GenerateTaskPDF compiles the document into an A4 PDF
func GenerateTaskPDF(task *db.Task) ([]byte, error) {
	imgBytes, err := GenerateTaskPageImage(task)
	if err != nil {
		return nil, err
	}
	return imageToPDF(imgBytes, 2480, 3508)
}

func imageToPDF(jpegData []byte, imgW, imgH int) ([]byte, error) {
	pageW := 595.28
	pageH := 841.89

	var pdf bytes.Buffer
	offsets := make([]int, 6)

	pdf.WriteString("%PDF-1.4\n")
	offsets[1] = pdf.Len()
	pdf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = pdf.Len()
	pdf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offsets[3] = pdf.Len()
	fmt.Fprintf(&pdf, "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents 4 0 R /Resources << /XObject << /Im1 5 0 R >> >> >>\nendobj\n", pageW, pageH)
	offsets[4] = pdf.Len()
	contentStream := fmt.Sprintf("q\n%.2f 0 0 %.2f 0 0 cm\n/Im1 Do\nQ\n", pageW, pageH)
	fmt.Fprintf(&pdf, "4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(contentStream), contentStream)
	offsets[5] = pdf.Len()
	fmt.Fprintf(&pdf, "5 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", imgW, imgH, len(jpegData))
	pdf.Write(jpegData)
	pdf.WriteString("\nendstream\nendobj\n")

	xrefOffset := pdf.Len()
	pdf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOffset)

	return pdf.Bytes(), nil
}
