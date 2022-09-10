package painter

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"sync"

	"github.com/go-text/typesetting/di"
	gotext "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	"github.com/goki/freetype"
	"github.com/goki/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/theme"
)

const (
	// DefaultTabWidth is the default width in spaces
	DefaultTabWidth = 4

	// TextDPI is a global constant that determines how text scales to interface sizes
	TextDPI = 78
)

// CachedFontFace returns a font face held in memory. These are loaded from the current theme.
func CachedFontFace(style fyne.TextStyle, opts *truetype.Options) (font.Face, gotext.Face) {
	val, ok := fontCache.Load(style)
	if !ok {
		var f1, f2 *truetype.Font
		switch {
		case style.Monospace:
			f1 = loadFont(theme.TextMonospaceFont())
			f2 = loadFont(theme.DefaultTextMonospaceFont())
		case style.Bold:
			if style.Italic {
				f1 = loadFont(theme.TextBoldItalicFont())
				f2 = loadFont(theme.DefaultTextBoldItalicFont())
			} else {
				f1 = loadFont(theme.TextBoldFont())
				f2 = loadFont(theme.DefaultTextBoldFont())
			}
		case style.Italic:
			f1 = loadFont(theme.TextItalicFont())
			f2 = loadFont(theme.DefaultTextItalicFont())
		case style.Symbol:
			f2 = loadFont(theme.DefaultSymbolFont())
		default:
			f1 = loadFont(theme.TextFont())
			f2 = loadFont(theme.DefaultTextFont())
		}

		if f1 == nil {
			f1 = f2
		}
		val = &fontCacheItem{font: f1, fallback: f2, faces: make(map[truetype.Options]font.Face),
			measureFaces: make(map[truetype.Options]gotext.Face)}
		fontCache.Store(style, val)
	}

	comp := val.(*fontCacheItem)
	comp.facesMutex.RLock()
	face := comp.faces[*opts]
	measureFace := comp.measureFaces[*opts]
	comp.facesMutex.RUnlock()
	if face == nil {
		f1 := truetype.NewFace(comp.font, opts)
		f2 := truetype.NewFace(comp.fallback, opts)
		face = newFontWithFallback(f1, f2, comp.font, comp.fallback)

		switch {
		case style.Monospace:
			measureFace = loadMeasureFont(theme.TextMonospaceFont())
		case style.Bold:
			if style.Italic {
				measureFace = loadMeasureFont(theme.TextBoldItalicFont())
			} else {
				measureFace = loadMeasureFont(theme.TextBoldFont())
			}
		case style.Italic:
			measureFace = loadMeasureFont(theme.TextItalicFont())
		default:
			measureFace = loadMeasureFont(theme.TextFont())
		}

		comp.facesMutex.Lock()
		comp.faces[*opts] = face
		comp.measureFaces[*opts] = measureFace
		comp.facesMutex.Unlock()
	}

	return face, measureFace
}

// ClearFontCache is used to remove cached fonts in the case that we wish to re-load font faces
func ClearFontCache() {
	fontCache.Range(func(_, val interface{}) bool {
		item := val.(*fontCacheItem)
		for _, face := range item.faces {
			err := face.Close()

			if err != nil {
				fyne.LogError("failed to close font face", err)
				return false
			}
		}
		return true
	})

	fontCache = &sync.Map{}
}

// DrawString draws a string into an image.
func DrawString(dst draw.Image, s string, color color.Color, f font.Face, face gotext.Face, fontSize, scale float32,
	height int, tabWidth int) {
	src := &image.Uniform{C: color}
	dot := freetype.Pt(0, height-f.Metrics().Descent.Ceil())
	walkString(face, s, float32ToFixed266(fontSize), tabWidth, &dot.X, scale, func(r rune) {
		dr, mask, maskp, _, ok := f.Glyph(dot, r)
		if ok {
			draw.DrawMask(dst, dr, src, image.Point{}, mask, maskp, draw.Over)
		}
	})
}

func loadMeasureFont(data fyne.Resource) gotext.Face {
	loaded, err := gotext.ParseTTF(bytes.NewReader(data.Content()))
	if err != nil {
		fyne.LogError("font load error", err)
	}

	return loaded
}

// MeasureString returns how far dot would advance by drawing s with f.
// Tabs are translated into a dot location change.
func MeasureString(f gotext.Face, s string, textSize fixed.Int26_6, tabWidth int) (size fyne.Size, advance fixed.Int26_6) {
	return walkString(f, s, textSize, tabWidth, &advance, 1, func(r rune) {})
}

// RenderedTextSize looks up how big a string would be if drawn on screen.
// It also returns the distance from top to the text baseline.
func RenderedTextSize(text string, fontSize float32, style fyne.TextStyle) (size fyne.Size, baseline float32) {
	size, base := cache.GetFontMetrics(text, fontSize, style)
	if base != 0 {
		return size, base
	}

	size, base = measureText(text, float32ToFixed266(fontSize), style)
	cache.SetFontMetrics(text, fontSize, style, size, base)
	return size, base
}

func fixed266ToFloat32(i fixed.Int26_6) float32 {
	return float32(float64(i) / (1 << 6))
}

func float32ToFixed266(f float32) fixed.Int26_6 {
	return fixed.Int26_6(float64(f) * (1 << 6))
}

func loadFont(data fyne.Resource) *truetype.Font {
	loaded, err := truetype.Parse(data.Content())
	if err != nil {
		fyne.LogError("font load error", err)
	}

	return loaded
}

func measureText(text string, fontSize fixed.Int26_6, style fyne.TextStyle) (fyne.Size, float32) {
	var opts truetype.Options
	opts.Size = float64(fontSize)
	opts.DPI = TextDPI

	_, face := CachedFontFace(style, &opts)
	size, base := MeasureString(face, text, fontSize, style.TabWidth)
	return size, fixed266ToFloat32(base)
}

func newFontWithFallback(chosen, fallback font.Face, chosenFont, fallbackFont ttfFont) font.Face {
	return &compositeFace{chosen: chosen, fallback: fallback, chosenFont: chosenFont, fallbackFont: fallbackFont}
}

func tabStop(f font.Face, x fixed.Int26_6, tabWidth int) fixed.Int26_6 {
	spacew, ok := f.GlyphAdvance(' ')
	if !ok {
		log.Print("Failed to find space width for tab")
		return x
	}

	return tabStopForSpaceWidth(spacew, x, tabWidth)
}

func tabStopForSpaceWidth(spacew fixed.Int26_6, x fixed.Int26_6, tabWidth int) fixed.Int26_6 {
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}

	tabw := spacew * fixed.Int26_6(tabWidth)
	tabs, _ := math.Modf(float64((x + tabw) / tabw))
	return tabw * fixed.Int26_6(tabs)
}

func walkString(f gotext.Face, s string, textSize fixed.Int26_6, tabWidth int, advance *fixed.Int26_6, scale float32, cb func(r rune)) (size fyne.Size, base fixed.Int26_6) {
	runes := []rune(s)
	in := shaping.Input{
		Text:      []rune{' '},
		RunStart:  0,
		RunEnd:    1,
		Direction: di.DirectionLTR,
		Face:      f,
		Size:      textSize,
	}
	out, _ := shaping.Shape(in)
	spacew := out.Advance

	in.Text = runes
	in.RunStart = 0
	in.RunEnd = len(runes)
	out, _ = shaping.Shape(in)

	for i, c := range runes {
		if c == '\r' {
			continue
		}
		if c == '\t' {
			*advance += tabStopForSpaceWidth(spacew, *advance, tabWidth)
		} else {
			cb(c)
			*advance += float32ToFixed266(fixed266ToFloat32(out.Glyphs[i].XAdvance) * scale)
		}
	}

	return fyne.NewSize(fixed266ToFloat32(*advance), fixed266ToFloat32(out.LineBounds.LineHeight())),
		out.LineBounds.Ascent
}

type compositeFace struct {
	sync.Mutex

	chosen, fallback         font.Face
	chosenFont, fallbackFont ttfFont
}

func (c *compositeFace) Close() (err error) {
	c.Lock()
	defer c.Unlock()

	if c.chosen != nil {
		err = c.chosen.Close()
	}

	err2 := c.fallback.Close()
	if err2 != nil {
		return err2
	}

	return
}

func (c *compositeFace) Glyph(dot fixed.Point26_6, r rune) (
	dr image.Rectangle, mask image.Image, maskp image.Point, advance fixed.Int26_6, ok bool) {
	c.Lock()
	defer c.Unlock()

	if c.containsGlyph(c.chosenFont, r) {
		return c.chosen.Glyph(dot, r)
	}

	if c.containsGlyph(c.fallbackFont, r) {
		return c.fallback.Glyph(dot, r)
	}

	return
}

func (c *compositeFace) GlyphAdvance(r rune) (advance fixed.Int26_6, ok bool) {
	c.Lock()
	defer c.Unlock()

	if c.containsGlyph(c.chosenFont, r) {
		return c.chosen.GlyphAdvance(r)
	}

	if c.containsGlyph(c.fallbackFont, r) {
		return c.fallback.GlyphAdvance(r)
	}

	return
}

func (c *compositeFace) GlyphBounds(r rune) (bounds fixed.Rectangle26_6, advance fixed.Int26_6, ok bool) {
	c.Lock()
	defer c.Unlock()

	if c.containsGlyph(c.chosenFont, r) {
		return c.chosen.GlyphBounds(r)
	}

	if c.containsGlyph(c.fallbackFont, r) {
		return c.fallback.GlyphBounds(r)
	}

	return
}

func (c *compositeFace) Kern(r0, r1 rune) fixed.Int26_6 {
	c.Lock()
	defer c.Unlock()

	if c.containsGlyph(c.chosenFont, r0) && c.containsGlyph(c.chosenFont, r1) {
		return c.chosen.Kern(r0, r1)
	}

	return c.fallback.Kern(r0, r1)
}

func (c *compositeFace) Metrics() font.Metrics {
	c.Lock()
	defer c.Unlock()

	return c.chosen.Metrics()
}

func (c *compositeFace) containsGlyph(font ttfFont, r rune) bool {
	return font != nil && font.Index(r) != 0
}

type ttfFont interface {
	Index(rune) truetype.Index
}

type fontCacheItem struct {
	font, fallback *truetype.Font
	faces          map[truetype.Options]font.Face
	measureFaces   map[truetype.Options]gotext.Face
	facesMutex     sync.RWMutex
}

var fontCache = &sync.Map{} // map[fyne.TextStyle]*fontCacheItem
