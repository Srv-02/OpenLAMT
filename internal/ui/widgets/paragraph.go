package widgets

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Paragraph is a wrapped text of a fixed width with a given line height (the design's
// line-height 1.45 for the support text; widget.RichText has no line height). The dialogs have
// fixed widths, so the width is a field instead of being negotiated through layouts.
type Paragraph struct {
	widget.BaseWidget

	Text       string
	Color      fyne.ThemeColorName // color token, read on every refresh
	TextSize   float32
	Width      float32
	LineHeight float32
}

// NewParagraph returns a paragraph; call Refresh after changing a field.
func NewParagraph(text string, size, width, lineHeight float32, c fyne.ThemeColorName) *Paragraph {
	p := &Paragraph{Text: text, TextSize: size, Width: width, LineHeight: lineHeight, Color: c}
	p.ExtendBaseWidget(p)
	return p
}

// Lines returns the text broken into lines of at most Width.
func (p *Paragraph) Lines() []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(p.Text) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && fyne.MeasureText(next, p.TextSize, fyne.TextStyle{}).Width > p.Width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func (p *Paragraph) CreateRenderer() fyne.WidgetRenderer {
	r := &paragraphRenderer{p: p}
	r.Refresh()
	return r
}

type paragraphRenderer struct {
	p     *Paragraph
	lines []*canvas.Text
}

func (r *paragraphRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.p.Width, float32(len(r.p.Lines()))*r.p.LineHeight)
}

func (r *paragraphRenderer) Layout(fyne.Size) {
	for i, t := range r.lines {
		h := t.MinSize().Height
		t.Move(fyne.NewPos(0, float32(i)*r.p.LineHeight+(r.p.LineHeight-h)/2))
		t.Resize(fyne.NewSize(r.p.Width, h))
	}
}

func (r *paragraphRenderer) Refresh() {
	lines := r.p.Lines()
	for len(r.lines) < len(lines) {
		r.lines = append(r.lines, canvas.NewText("", color.Transparent))
	}
	r.lines = r.lines[:len(lines)]
	for i, s := range lines {
		t := r.lines[i]
		t.Text, t.Color, t.TextSize = s, style.Color(r.p.Color), r.p.TextSize
		t.Refresh()
	}
	r.Layout(r.p.BaseWidget.Size())
	canvas.Refresh(r.p)
}

func (r *paragraphRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(r.lines))
	for i, t := range r.lines {
		objs[i] = t
	}
	return objs
}
func (r *paragraphRenderer) Destroy() {}
