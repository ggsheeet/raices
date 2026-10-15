package mail

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	ticketWidth = 720
	ticketPad   = 48
	qrPixels    = 340
)

var (
	clrPaper = color.RGBA{246, 241, 230, 255}
	clrGreen = color.RGBA{82, 100, 90, 255}
	clrGold  = color.RGBA{214, 185, 120, 255}
	clrInk   = color.RGBA{41, 42, 38, 255}
	clrCream = color.RGBA{246, 241, 230, 255}
)

// TicketCard is the pass drawn into the email attachment.
type TicketCard struct {
	URL   string
	Name  string
	Index int
	Count int
}

type textLine struct {
	text  string
	scale int
	color color.RGBA
	gap   int
}

func renderTicketPNG(card TicketCard) ([]byte, error) {
	code, err := qrcode.New(card.URL, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	qrImg := code.Image(qrPixels)

	maxW := ticketWidth - ticketPad*2
	header := []textLine{
		{eventName, 4, clrCream, 12},
		{eventSeries, 3, clrCream, 8},
		{eventDates, 3, clrCream, 8},
	}
	place := wrapLines([]string{eventPlace, eventStreet, eventCity}, maxW, 3, clrGreen, 8)
	lower := []textLine{{ticketLabel(card), 4, clrInk, 14}}
	if name := strings.TrimSpace(card.Name); name != "" {
		lower = append(lower, wrapLines([]string{name}, maxW, 3, clrInk, 8)...)
	}
	footer := wrapLines([]string{"Para el personal de la puerta"}, maxW, 3, clrGreen, 8)

	const (
		headerTop  = 36
		headerBot  = 20
		gold       = 8
		beforeBody = 28
		afterBody  = 22
		dots       = 8
		beforeQR   = 22
		afterQR    = 28
		beforeFoot = 12
		bottom     = 40
	)
	headerH := headerTop + stackHeight(header) + headerBot
	height := headerH + gold + beforeBody + stackHeight(place) + afterBody + dots + beforeQR + qrPixels + afterQR + stackHeight(lower) + beforeFoot + stackHeight(footer) + bottom

	img := image.NewRGBA(image.Rect(0, 0, ticketWidth, height))
	fill(img, img.Bounds(), clrPaper)
	fill(img, image.Rect(0, 0, ticketWidth, headerH), clrGreen)
	fill(img, image.Rect(0, headerH, ticketWidth, headerH+gold), clrGold)

	y := headerTop
	y = drawLines(img, header, y)
	y = headerH + gold + beforeBody
	y = drawLines(img, place, y)
	y += afterBody
	drawDots(img, y)
	y += dots + beforeQR

	qx := (ticketWidth - qrPixels) / 2
	fill(img, image.Rect(qx-6, y-6, qx+qrPixels+6, y+qrPixels+6), clrGreen)
	draw.Draw(img, image.Rect(qx, y, qx+qrPixels, y+qrPixels), qrImg, image.Point{}, draw.Over)
	y += qrPixels + afterQR
	y = drawLines(img, lower, y)
	y += beforeFoot
	drawLines(img, footer, y)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ticketLabel(card TicketCard) string {
	if card.Index < 1 {
		return "Boleto"
	}
	if card.Count > 1 {
		return fmt.Sprintf("Boleto %d de %d", card.Index, card.Count)
	}
	return fmt.Sprintf("Boleto %d", card.Index)
}

func wrapLines(parts []string, maxW, scale int, c color.RGBA, gap int) []textLine {
	var lines []textLine
	for _, part := range parts {
		for _, line := range wrapText(part, maxW, scale) {
			lines = append(lines, textLine{line, scale, c, gap})
		}
	}
	return lines
}

func stackHeight(lines []textLine) int {
	h := 0
	for _, line := range lines {
		h += line.scale*8 + line.gap
	}
	return h
}

func drawLines(img *image.RGBA, lines []textLine, y int) int {
	for _, line := range lines {
		drawCentered(img, line.text, y, line.scale, line.color)
		y += line.scale*8 + line.gap
	}
	return y
}

func drawDots(img *image.RGBA, y int) {
	for x := 28; x < ticketWidth-28; x += 18 {
		fill(img, image.Rect(x, y, x+8, y+8), clrGold)
	}
}

func wrapText(s string, maxWidth, scale int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := ""
	for _, word := range words {
		if textWidth(word, scale) > maxWidth {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			lines = append(lines, breakLong(word, maxWidth, scale)...)
			continue
		}
		next := word
		if cur != "" {
			next = cur + " " + word
		}
		if textWidth(next, scale) <= maxWidth {
			cur = next
			continue
		}
		lines = append(lines, cur)
		cur = word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func breakLong(word string, maxWidth, scale int) []string {
	var lines []string
	var b strings.Builder
	for _, r := range word {
		next := b.String() + string(r)
		if b.Len() > 0 && textWidth(next, scale) > maxWidth {
			lines = append(lines, b.String())
			b.Reset()
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}

func textWidth(text string, scale int) int {
	n := 0
	for range text {
		n++
	}
	return n * 6 * scale
}

func drawCentered(img *image.RGBA, text string, y, scale int, c color.RGBA) {
	x := (img.Bounds().Dx() - textWidth(text, scale)) / 2
	if x < 0 {
		x = 0
	}
	drawText(img, text, x, y, scale, c)
}

func drawText(img *image.RGBA, text string, x, y, scale int, c color.RGBA) {
	for _, r := range text {
		rows, ok := glyph(r)
		if !ok {
			rows = [8]byte{0x00, 0x1F, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1F}
		}
		for row := 0; row < 8; row++ {
			bits := rows[row]
			for col := 0; col < 5; col++ {
				if bits&(1<<uint(4-col)) == 0 {
					continue
				}
				px := x + col*scale
				py := y + row*scale
				fill(img, image.Rect(px, py, px+scale, py+scale), c)
			}
		}
		x += 6 * scale
	}
}

func fill(img *image.RGBA, rect image.Rectangle, c color.RGBA) {
	draw.Draw(img, rect.Intersect(img.Bounds()), &image.Uniform{c}, image.Point{}, draw.Src)
}
