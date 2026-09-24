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
	w := min(108, max(1, width-margin*2))
	desired := max(18, len(f.renderRows())*2+12)
	if f.popupEditorHeight > 0 {
		desired = f.popupEditorHeight
	}
	available := height
	if height >= 16 {
		available -= 2
	}
	anchorHeight := min(available, max(30, f.structuralRowCount()*2+12))
	h := min(desired, available)
	l := draftPopupLayout{box: rect{(width - w) / 2, (height - anchorHeight) / 2, w, h}}
	l.inner = rect{l.box.x + 1, l.box.y + 1, max(1, w-2), max(1, h-2)}
	if w < 4 || h < 3 {
		l.inner = l.box
	}
	return l
}

func (f *Form) popupPanel() (rect, string) {
	if f.DraftPopupActive() {
		l := f.draftPopupLayout()
		panel := bordered("Job draft", strings.Split(f.View().Content, "\n"), l.box.w, l.box.h, true, f.dark)
		return l.box, panel
	}
	l := f.modalLayout()
	return l.box, f.modalPanel()
}

func (f *Form) reflowDraft() {
	if f.stage != "edit" || f.finished {
		return
	}
	visible := f.focusable()
	layout := f.rowSignature()
	if f.visibleLayout != layout {
		f.visibleLayout = layout
		f.clearPopupPress()
	}
	if len(visible) == 0 {
		f.blurField()
	} else if !f.fieldFocusable(f.focus) {
		nearest := visible[0]
		distance := len(f.inputs) + 1
		slot := ""
		if f.focus >= 0 && f.focus < len(f.spec.Fields) {
			slot = f.spec.Fields[f.focus].Slot
		}
		for _, index := range visible {
			if slot != "" && f.spec.Fields[index].Slot == slot {
				nearest = index
				break
			}
			delta := index - f.focus
			if delta < 0 {
				delta = -delta
			}
			if delta < distance {
				nearest, distance = index, delta
			}
		}
		if nearest != f.focus {
			f.focusField(nearest)
		}
	}
	if f.picker != nil && !f.fieldFocusable(f.picker.index) {
		f.closePicker()
	}
	if f.schedule != nil && !f.fieldFocusable(f.scheduleIndex) {
		f.schedule = nil
	}
	if f.multiline != nil && !f.fieldFocusable(f.multiline.index) {
		f.multiline = nil
	}
	if !f.DraftPopupActive() {
		f.ensureFieldViewport()
		return
	}
	nested := f.schedule != nil || f.picker != nil || f.help != nil || f.multiline != nil
	if nested && f.popupEditorHeight == 0 {
		// Freeze a roomy editing surface while child tools are open. Late
		// suggestions may change the draft's field count without moving them.
		f.popupEditorHeight = max(30, len(f.renderRows())*2+12)
	} else if !nested {
		f.popupEditorHeight = 0
	}
	inner := f.draftPopupLayout().inner
	if f.width != inner.w || f.height != inner.h {
		f.resizeForm(f.canvasWidth, f.canvasHeight)
	}
	f.ensureFieldViewport()
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
	f.ensureFieldViewport()
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
