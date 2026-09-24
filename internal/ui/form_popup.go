package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type draftPopupLayout struct{ box, inner rect }

func (f *Form) DraftPopupActive() bool {
	return f.spec.Popup && f.embedded && f.popupHost && f.stage == "edit" && !f.initialReview
}

// SetPopupHost is enabled only by a container that composites popupPanel and
// translates popupInput. Other embedded workflows retain their full surface.
func (f *Form) SetPopupHost(enabled bool) {
	f.popupHost = enabled
	f.resizeForm(f.canvasWidth, f.canvasHeight)
}

func (f *Form) canvasSize() (int, int) {
	if f.spec.Popup && f.embedded && f.popupHost {
		return max(1, f.canvasWidth), max(1, f.canvasHeight)
	}
	return f.width, f.height
}

func (f *Form) draftPopupLayout() draftPopupLayout {
	width, height := max(1, f.canvasWidth), max(1, f.canvasHeight)
	margin := 0
	if width >= 60 {
		margin = 4
	} else if width >= 16 {
		margin = 1
	}
	w, h := min(108, max(1, width-margin*2)), min(30, height)
	if height >= 16 {
		h = min(h, height-2)
	}
	l := draftPopupLayout{box: rect{(width - w) / 2, (height - h) / 2, w, h}}
	l.inner = rect{l.box.x + 1, l.box.y + 1, max(1, w-2), max(1, h-2)}
	if w < 4 || h < 3 {
		l.inner = l.box
	}
	return l
}

func (f *Form) popupPanel() (rect, string) {
	if f.DraftPopupActive() {
		l := f.draftPopupLayout()
		panel := bordered("Job draft · Alt+1/2/3 switch views", strings.Split(f.View().Content, "\n"), l.box.w, l.box.h, true, f.dark)
		return l.box, panel
	}
	l := f.modalLayout()
	return l.box, f.modalPanel()
}

func (f *Form) resizeForm(width, height int) {
	f.canvasWidth, f.canvasHeight = max(1, width), max(1, height)
	f.width, f.height = f.canvasWidth, f.canvasHeight
	if f.spec.Popup && f.embedded && f.popupHost {
		inner := f.draftPopupLayout().inner
		f.width, f.height = inner.w, inner.h
	}
	f.mousePress = ""
	for i := range f.inputs {
		f.inputs[i].SetWidth(max(1, f.width-8))
	}
	if f.picker != nil {
		f.picker.press = ""
		f.picker.query.SetWidth(max(1, f.width-6))
	}
	if f.schedule != nil {
		f.schedule.SetSize(f.width, f.height)
	}
	if f.help != nil {
		f.help.Update(tea.WindowSizeMsg{Width: f.width, Height: f.height})
	}
	if f.multiline != nil {
		f.resizeMultiline()
	}
}

func (f *Form) clearPopupPress() {
	f.mousePress = ""
	if f.picker != nil {
		f.picker.press = ""
	}
	if f.help != nil {
		f.help.press = ""
	}
	if f.schedule != nil {
		f.schedule.pressed = ""
	}
	if f.multiline != nil {
		f.multiline.press = ""
	}
}

func (f *Form) popupInput(msg tea.Msg) (tea.Msg, bool) {
	if !f.DraftPopupActive() {
		return msg, true
	}
	inner := f.draftPopupLayout().inner
	x, y, mouse := 0, 0, true
	switch value := msg.(type) {
	case tea.MouseClickMsg:
		x, y = value.X, value.Y
	case tea.MouseReleaseMsg:
		x, y = value.X, value.Y
	case tea.MouseWheelMsg:
		x, y = value.X, value.Y
	case tea.MouseMotionMsg:
		x, y = value.X, value.Y
	default:
		mouse = false
	}
	if mouse && !inner.contains(x, y) {
		f.clearPopupPress()
		return nil, false
	}
	return offsetMouseXY(msg, -inner.x, -inner.y), true
}
