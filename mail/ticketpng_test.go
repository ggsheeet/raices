package mail

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestRenderTicketPNG(t *testing.T) {
	sample := "Raíces 2026 Del Pulpito al Hogar 30 oct – 1 nov INP Emanuel Dios con Nosotros Av Insurgentes 450, San Jerónimo 64630 Monterrey N.L. Boleto de Para el personal de la puerta Ada Lovelace José Núñez-O'Hara"
	for _, r := range sample {
		if _, ok := glyph(r); !ok {
			t.Fatalf("missing glyph %q", string(r))
		}
	}

	raw, err := renderTicketPNG(TicketCard{
		URL:   "http://tickets.example/tickets/claim/abc",
		Name:  "José Núñez-O'Hara",
		Index: 2,
		Count: 3,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(raw, pngMagic) {
		t.Fatal("not a PNG")
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != ticketWidth || img.Bounds().Dy() < 700 {
		t.Fatalf("size %v", img.Bounds())
	}

	var headerInk, green, black int
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			rr, gg, bb := r>>8, g>>8, bl>>8
			switch {
			case rr == uint32(clrGreen.R) && gg == uint32(clrGreen.G) && bb == uint32(clrGreen.B):
				green++
			case rr < 30 && gg < 30 && bb < 30:
				black++
			}
			if y >= 24 && y < 150 && rr == uint32(clrCream.R) && gg == uint32(clrCream.G) && bb == uint32(clrCream.B) {
				headerInk++
			}
		}
	}
	if green < 1000 || black < 1000 || headerInk < 40 {
		t.Fatalf("green %d black %d header text %d", green, black, headerInk)
	}
}

func TestTicketLabel(t *testing.T) {
	if got := ticketLabel(TicketCard{Index: 2, Count: 3}); got != "Boleto 2 de 3" {
		t.Fatalf("%q", got)
	}
	if got := ticketLabel(TicketCard{Index: 1, Count: 1}); got != "Boleto 1" {
		t.Fatalf("%q", got)
	}
	if strings.Contains(ticketLabel(TicketCard{}), "de") {
		t.Fatal("blank index should not invent a count")
	}
}
