package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var ErrCancelled = errors.New("cancelled")

type Field struct {
	Key, Label, Value string
	Kind, Hint        string
	Pick              func(context.Context, map[string]string) ([]PickOption, error)
	Options           []string
	OptionLabels      map[string]string
	Advanced          bool
	Unavailable       map[string]string
	Show              func(map[string]string) bool
}
type FieldUpdate struct {
	Key         string
	Options     []string
	Unavailable map[string]string
	Value       *string
	Hint        string
	Schedule    *ScheduleEditorOptions
}

func (f Field) displayValue(value string) string {
	if label, ok := f.OptionLabels[value]; ok {
		return label
	}
	return value
}

type PickOption struct {
	Label, Value string
	Navigate     bool
}
type Review struct {
	Text string
	Diff string
	Data any
}

// Body is the plain-text equivalent for CLI output and other linear surfaces.
func (r Review) Body() string {
	if r.Diff == "" {
		return r.Text
	}
	if r.Text == "" {
		return r.Diff
	}
	return strings.TrimRight(r.Text, "\n") + "\n\n" + r.Diff
}

type FormSpec struct {
	Title           string
	Theme           string
	Fields          []Field
	Live            func(map[string]string) string
	Build           func(context.Context, map[string]string) (Review, error)
	Apply           func(context.Context, map[string]string, Review) (string, error)
	Mouse           bool
	Load            func(context.Context, map[string]string) []FieldUpdate
	LoadKeys        []string
	ScheduleContext func(map[string]string) ScheduleEditorOptions
	TextEditor      func(string) *exec.Cmd
	Popup           bool
}
type FormResult struct {
	Values    map[string]string
	Review    Review
	Message   string
	Submitted bool
}
type formBuilt struct {
	review Review
	err    error
	owner  *Form
}
type formApplied struct {
	message string
	err     error
	owner   *Form
}
type formLoaded struct {
	generation int
	fields     []FieldUpdate
}
type formLoadTick struct {
	owner      *Form
	generation int
}
type Form struct {
	spec                         FormSpec
	ctx                          context.Context
	cancel                       context.CancelFunc
	inputs                       []textinput.Model
	focus, width, height, scroll int
	canvasWidth, canvasHeight    int
	advanced                     bool
	stage                        string
	review                       Review
	message                      string
	err                          error
	result                       FormResult
	mousePress                   string
	loadGeneration               int
	loadCancel                   context.CancelFunc
	loadValues                   map[string]string
	embedded, dark               bool
	popupHost                    bool
	popupEditorHeight            int
	visibleLayout                string
	live                         string
	liveGeneration               int
	schedule                     *ScheduleEditor
	scheduleIndex                int
	scheduleContext              *ScheduleEditorOptions
	picker                       *formPicker
	help                         *HelpBrowser
	initialReview                bool
	applyDispatched              bool
	finished                     bool
	cancelRequested              bool
	multilineValues              map[int]string
	multiline                    *multilineDraft
	presentation                 reviewPresentation
}
type formLive struct {
	generation int
	text       string
}

var formSequence atomic.Int64

func (f *Form) SetEmbedded(value bool) {
	f.embedded = value
	f.resizeForm(f.canvasWidth, f.canvasHeight)
}
func (f *Form) SetMouse(value bool) {
	f.spec.Mouse = value
	f.clearPopupPress()
	if f.schedule != nil {
		f.schedule.SetMouse(value)
	}
	if f.help != nil {
		f.help.SetMouse(value)
	}
}
func (f *Form) finish() tea.Cmd {
	if f.finished {
		return nil
	}
	f.finished = true
	f.cancel()
	if f.embedded {
		result := WorkflowDoneMsg{Err: f.err, Message: f.message, Changed: f.result.Submitted || f.applyDispatched, Owner: f}
		if f.stage == "applying" {
			result.Changed = true
			result.Message = "Stopped waiting; a dispatched operation may already have taken effect."
		}
		return func() tea.Msg { return result }
	}
	return tea.Quit
}
func (f *Form) Result() (FormResult, error) { return f.result, f.err }

func NewForm(ctx context.Context, spec FormSpec) *Form {
	child, cancel := context.WithCancel(ctx)
	f := &Form{spec: spec, ctx: child, cancel: cancel, width: 80, height: 24, canvasWidth: 80, canvasHeight: 24, stage: "edit", dark: spec.Theme != "light"}
	for _, field := range spec.Fields {
		input := textinput.New()
		if field.Kind == "multiline" {
			if f.multilineValues == nil {
				f.multilineValues = map[int]string{}
			}
			f.multilineValues[len(f.inputs)] = field.Value
		} else {
			input.SetValue(field.Value)
		}
		input.Prompt = ""
		input.SetWidth(60)
		input.CharLimit = 65536
		input.SetVirtualCursor(true)
		f.inputs = append(f.inputs, input)
	}
	if len(f.inputs) > 0 {
		f.inputs[0].Focus()
	}
	f.visibleLayout = fmt.Sprint(f.visible())
	return f
}
func (f *Form) Init() tea.Cmd {
	if f.initialReview {
		return tea.Batch(tea.RequestBackgroundColor, f.reviewCmd())
	}
	return tea.Batch(textinput.Blink, tea.RequestBackgroundColor, f.load(), f.updateLive())
}
func NewReviewForm(ctx context.Context, spec FormSpec) *Form {
	f := NewForm(ctx, spec)
	f.initialReview = true
	return f
}
func (f *Form) load() tea.Cmd {
	if f.spec.Load == nil {
		return nil
	}
	f.loadGeneration = int(formSequence.Add(1))
	return f.startLoad()
}
func (f *Form) startLoad() tea.Cmd {
	if f.loadCancel != nil {
		f.loadCancel()
	}
	ctx, cancel := context.WithCancel(f.ctx)
	f.loadCancel = cancel
	g := f.loadGeneration
	v := f.Values()
	f.loadValues = v
	load := f.spec.Load
	return func() tea.Msg { return formLoaded{g, load(ctx, v)} }
}
func (f *Form) queueLoad() tea.Cmd {
	if f.spec.Load == nil {
		return nil
	}
	if f.loadCancel != nil {
		f.loadCancel()
	}
	f.loadGeneration = int(formSequence.Add(1))
	g := f.loadGeneration
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return formLoadTick{f, g} })
}
func (f *Form) Values() map[string]string {
	v := map[string]string{}
	for i, field := range f.spec.Fields {
		v[field.Key] = f.fieldValue(i)
	}
	return v
}
func (f *Form) visible() []int {
	out := []int{}
	values := f.Values()
	for i, v := range f.spec.Fields {
		if (!v.Advanced || f.advanced) && (v.Show == nil || v.Show(values)) {
			out = append(out, i)
		}
	}
	return out
}
func (f *Form) move(delta int) {
	list := f.visible()
	if len(list) == 0 {
		return
	}
	pos := 0
	for i, n := range list {
		if n == f.focus {
			pos = i
		}
	}
	f.inputs[f.focus].Blur()
	f.focus = list[(pos+delta+len(list))%len(list)]
	f.inputs[f.focus].Focus()
}
func (f *Form) reviewCmd() tea.Cmd {
	f.mousePress = ""
	f.err = nil
	f.stage = "building"
	values := f.Values()
	return func() tea.Msg { r, e := f.spec.Build(f.ctx, values); return formBuilt{r, e, f} }
}
func (f *Form) applyCmd() tea.Cmd {
	f.mousePress = ""
	f.stage = "applying"
	f.applyDispatched = true
	values, review := f.Values(), f.review
	return func() tea.Msg {
		message, err := f.spec.Apply(f.ctx, values, review)
		return formApplied{message, err, f}
	}
}
func (f *Form) updateLive() tea.Cmd {
	if f.spec.Live == nil {
		return nil
	}
	f.liveGeneration = int(formSequence.Add(1))
	g := f.liveGeneration
	values := f.Values()
	live := f.spec.Live
	return func() tea.Msg { return formLive{g, live(values)} }
}
func (f *Form) changed(key string) tea.Cmd {
	f.mousePress = ""
	f.err = nil
	cmds := []tea.Cmd{f.updateLive()}
	for _, k := range f.spec.LoadKeys {
		if k == key {
			cmds = append(cmds, f.queueLoad())
			break
		}
	}
	if key == "host" && len(f.spec.LoadKeys) == 0 {
		cmds = append(cmds, f.queueLoad())
	}
	if key == "host" || key == "source" {
		f.scheduleContext = nil
	}
	f.reflowDraft()
	return tea.Batch(cmds...)
}
func (f *Form) focusField(index int) {
	if index < 0 || index >= len(f.inputs) {
		return
	}
	if len(f.inputs) > 0 {
		f.inputs[f.focus].Blur()
	}
	f.focus = index
	f.inputs[index].Focus()
	f.mousePress = ""
}
func (f *Form) chooseOption(delta int) tea.Cmd {
	if len(f.inputs) == 0 {
		return nil
	}
	field := f.spec.Fields[f.focus]
	options := field.Options
	if len(options) == 0 {
		return nil
	}
	pos := 0
	for i, v := range options {
		if v == f.inputs[f.focus].Value() {
			pos = i
		}
	}
	pos = (pos + delta + len(options)) % len(options)
	if reason := field.Unavailable[options[pos]]; reason != "" {
		f.err = fmt.Errorf("%s", reason)
		return nil
	}
	f.inputs[f.focus].SetValue(options[pos])
	return f.changed(field.Key)
}
func (f *Form) openSchedule() tea.Cmd {
	options := ScheduleEditorOptions{Timezone: "Local", Locale: "en", Mouse: f.spec.Mouse}
	if f.spec.ScheduleContext != nil {
		options = f.spec.ScheduleContext(f.Values())
	}
	if f.scheduleContext != nil {
		options = *f.scheduleContext
	}
	options.Expression = f.inputs[f.focus].Value()
	options.Mouse = f.spec.Mouse
	f.scheduleIndex = f.focus
	f.schedule = NewScheduleEditor(f.ctx, options)
	f.schedule.SetSize(f.width, f.height)
	return f.schedule.Init()
}
func (f *Form) closeSchedule() tea.Cmd {
	i := f.scheduleIndex
	f.inputs[i].SetValue(f.schedule.Expression())
	f.schedule = nil
	return f.changed(f.spec.Fields[i].Key)
}
func (f *Form) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if f.finished {
		if ready, ok := msg.(textEditorReady); ok && ready.cleanup != nil {
			ready.cleanup()
		}
		return f, nil
	}
	defer f.reflowDraft()
	switch m := msg.(type) {
	case textEditorReady:
		return f, f.launchTextEditor(m)
	case textEditorResult:
		return f, f.receiveTextEditor(m)
	case formLoadTick:
		if m.owner == f && m.generation == f.loadGeneration && f.stage == "edit" {
			return f, f.startLoad()
		}
		return f, nil
	case tea.BackgroundColorMsg:
		if f.spec.Theme == "" || f.spec.Theme == "auto" {
			f.dark = m.IsDark()
		}
		if f.multiline != nil {
			f.resizeMultiline()
		}
		return f, nil
	case tea.WindowSizeMsg:
		f.resizeForm(m.Width, m.Height)
		return f, nil
	case formBuilt:
		if m.owner != f || f.stage != "building" || f.finished {
			return f, nil
		}
		if m.err != nil {
			f.err = m.err
			if f.initialReview {
				f.stage = "result"
				f.message = "Review could not be prepared. No write was sent."
			} else {
				f.stage = "edit"
			}
		} else {
			f.review = m.review
			f.stage = "review"
			f.scroll = 0
		}
		return f, nil
	case formApplied:
		if m.owner != f || f.stage != "applying" || f.finished {
			return f, nil
		}
		f.message = m.message
		f.err = m.err
		f.stage = "result"
		f.scroll = 0
		f.result = FormResult{Values: f.Values(), Review: f.review, Message: m.message, Submitted: m.err == nil}
		return f, nil
	case formLive:
		if m.generation == f.liveGeneration {
			f.live = m.text
		}
		return f, nil
	case formLoaded:
		if m.generation != f.loadGeneration || f.stage != "edit" {
			return f, nil
		}
		f.clearPopupPress()
		var updates []tea.Cmd
		for _, u := range m.fields {
			for i := range f.spec.Fields {
				if f.spec.Fields[i].Key == u.Key {
					if u.Options != nil {
						f.spec.Fields[i].Options = u.Options
					}
					f.spec.Fields[i].Unavailable = u.Unavailable
					if u.Value != nil && f.fieldValue(i) == f.loadValues[u.Key] {
						f.setFieldValue(i, *u.Value)
					}
					f.spec.Fields[i].Hint = u.Hint
					if u.Schedule != nil {
						f.scheduleContext = u.Schedule
						if f.schedule != nil {
							updates = append(updates, f.schedule.SetContext(u.Schedule.Dialect, u.Schedule.Timezone, u.Schedule.Locale))
						}
					}
				}
			}
		}
		return f, tea.Batch(append(updates, f.updateLive())...)
	case formPickLoaded:
		return f, f.acceptPick(m)
	}
	if f.help != nil {
		if closed, ok := msg.(helpClosedMsg); ok && closed.owner == f.help {
			f.help = nil
			return f, nil
		}
		_, cmd := f.help.Update(msg)
		return f, cmd
	}
	if f.picker != nil {
		return f, f.updatePicker(msg)
	}
	if f.multiline != nil {
		return f, f.updateMultiline(msg)
	}
	if f.schedule != nil {
		switch m := msg.(type) {
		case ScheduleUseMsg:
			return f, f.closeSchedule()
		case tea.KeyPressMsg:
			if m.String() == "esc" && !f.schedule.Editing() {
				return f, f.closeSchedule()
			}
			if m.String() == "ctrl+c" {
				return f, f.closeSchedule()
			}
		}
		_, cmd := f.schedule.Update(msg)
		return f, cmd
	}
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		if !f.spec.Mouse {
			return f, nil
		}
		mouse := m.Mouse()
		id := hitAt(f.hits(), mouse.X, mouse.Y)
		f.mousePress = ""
		if strings.HasPrefix(id, "field:") {
			var index int
			fmt.Sscanf(id, "field:%d", &index)
			f.focusField(index)
		} else {
			f.mousePress = id
		}
		return f, nil
	case tea.MouseReleaseMsg:
		if !f.spec.Mouse {
			return f, nil
		}
		mouse := m.Mouse()
		id := hitAt(f.hits(), mouse.X, mouse.Y)
		pressed := f.mousePress
		f.mousePress = ""
		if id != "" && id == pressed {
			return f, f.activate(id)
		}
		return f, nil
	case tea.MouseWheelMsg:
		if !f.spec.Mouse {
			return f, nil
		}
		delta := 1
		if strings.Contains(m.String(), "up") {
			delta = -1
		}
		if f.stage == "edit" && len(f.inputs) > 0 {
			f.move(delta)
		} else {
			f.scrollReview(delta * 3)
		}
		f.mousePress = ""
		return f, nil
	case tea.KeyPressMsg:
		key := m.String()
		f.mousePress = ""
		if m.IsRepeat && (key == "ctrl+s" || key == "y") {
			return f, nil
		}
		if key == "ctrl+c" {
			if f.stage == "applying" {
				f.cancelRequested = true
				f.cancel()
				return f, nil
			}
			f.err = ErrCancelled
			return f, f.finish()
		}
		if f.stage == "result" {
			if key == "enter" || key == "q" || key == "esc" {
				return f, f.finish()
			}
			f.reviewScrollKey(key)
			return f, nil
		}
		if f.stage == "building" || f.stage == "applying" {
			if key == "esc" {
				if f.stage == "applying" {
					f.cancelRequested = true
					f.cancel()
					return f, nil
				}
				f.err = ErrCancelled
				return f, f.finish()
			}
			return f, nil
		}
		if f.stage == "review" {
			switch key {
			case "ctrl+s", "y":
				return f, f.applyCmd()
			case "esc", "n":
				return f, f.activate("back")
			case "down", "j":
				f.scrollReview(1)
			case "up", "k":
				f.scrollReview(-1)
			case "pgdown":
				f.scrollReview(f.modalLayout().body.h)
			case "pgup":
				f.scrollReview(-f.modalLayout().body.h)
			case "home", "g":
				f.scroll = 0
			case "end", "G":
				f.scrollReview(len(f.reviewLines()))
			}
			return f, nil
		}
		switch key {
		case "esc":
			f.err = ErrCancelled
			return f, f.finish()
		case "ctrl+s":
			return f, f.reviewCmd()
		case "ctrl+o":
			return f, f.activate("advanced")
		case "ctrl+p", "f4":
			return f, f.startPicker()
		case "f1":
			topic := "paths-and-scripts"
			if len(f.inputs) > 0 {
				switch f.spec.Fields[f.focus].Key {
				case "host", "source":
					topic = "ssh"
				case "schedule":
					topic = "cron-fields"
				case "environment", "runner":
					topic = "execution-environment"
				case "project":
					topic = "uv"
				}
			}
			f.help = NewHelpBrowser(topic, f.spec.Mouse)
			f.help.SetEmbedded(true)
			f.help.nested = true
			f.help.Update(tea.WindowSizeMsg{Width: f.width, Height: f.height})
			return f, f.help.Init()
		case "enter":
			if len(f.inputs) > 0 && f.spec.Fields[f.focus].Kind == "multiline" {
				return f, f.openMultiline()
			}
			if len(f.inputs) > 0 && f.spec.Fields[f.focus].Kind == "schedule" {
				return f, f.openSchedule()
			}
			f.move(1)
			return f, nil
		case "tab":
			f.move(1)
			return f, nil
		case "shift+tab":
			f.move(-1)
			return f, nil
		}
		if len(f.inputs) > 0 && len(f.spec.Fields[f.focus].Options) == 0 {
			if key == "up" || key == "down" {
				delta := 1
				if key == "up" {
					delta = -1
				}
				f.move(delta)
				return f, nil
			}
		}
		if len(f.inputs) > 0 && len(f.spec.Fields[f.focus].Options) > 0 {
			switch key {
			case "left", "up", "h", "k":
				return f, f.chooseOption(-1)
			case "right", "down", "l", "j", "space":
				return f, f.chooseOption(1)
			}
			return f, nil
		}
		if len(f.inputs) > 0 && (f.spec.Fields[f.focus].Kind == "schedule" || f.spec.Fields[f.focus].Kind == "multiline") {
			return f, nil
		}
	}
	if f.stage == "edit" && len(f.inputs) > 0 {
		before := f.inputs[f.focus].Value()
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		if before != f.inputs[f.focus].Value() {
			return f, tea.Batch(cmd, f.changed(f.spec.Fields[f.focus].Key))
		}
		return f, cmd
	}
	return f, nil
}
func (f *Form) activate(id string) tea.Cmd {
	switch id {
	case "review":
		if f.stage == "edit" {
			return f.reviewCmd()
		}
	case "apply":
		if f.stage == "review" {
			return f.applyCmd()
		}
	case "back":
		if len(f.inputs) == 0 {
			f.err = ErrCancelled
			return f.finish()
		}
		f.stage = "edit"
		f.scroll = 0
		return f.load()
	case "cancel":
		if f.stage == "applying" {
			f.cancelRequested = true
			f.cancel()
			return nil
		}
		f.err = ErrCancelled
		return f.finish()
	case "return":
		return f.finish()
	case "advanced":
		before := map[int]bool{}
		for _, index := range f.visible() {
			before[index] = true
		}
		f.advanced = !f.advanced
		if f.advanced {
			for _, index := range f.visible() {
				if f.spec.Fields[index].Advanced && !before[index] {
					f.focusField(index)
					break
				}
			}
		}
		f.clearPopupPress()
		f.reflowDraft()
	case "browse":
		return f.startPicker()
	default:
		var index int
		if strings.HasPrefix(id, "prev:") {
			fmt.Sscanf(id, "prev:%d", &index)
			f.focusField(index)
			return f.chooseOption(-1)
		}
		if strings.HasPrefix(id, "next:") {
			fmt.Sscanf(id, "next:%d", &index)
			f.focusField(index)
			return f.chooseOption(1)
		}
		if strings.HasPrefix(id, "schedule:") {
			fmt.Sscanf(id, "schedule:%d", &index)
			f.focusField(index)
			return f.openSchedule()
		}
		if strings.HasPrefix(id, "multiline:") {
			fmt.Sscanf(id, "multiline:%d", &index)
			f.focusField(index)
			return f.openMultiline()
		}
		if strings.HasPrefix(id, "browse:") {
			fmt.Sscanf(id, "browse:%d", &index)
			f.focusField(index)
			return f.startPicker()
		}
	}
	return nil
}
func (f *Form) fieldWindow() ([]int, int, int) {
	list := f.visible()
	rows := max(1, (f.height-10)/2)
	pos := 0
	for i, n := range list {
		if n == f.focus {
			pos = i
		}
	}
	start := max(0, pos-rows+1)
	return list, start, rows
}
func safe(s string) string {
	var b strings.Builder
	for _, r := range ansi.Strip(s) {
		if r == '\n' || r == '\t' || r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Plain removes terminal control sequences from human-facing external text.
func Plain(s string) string           { return safe(s) }
func clip(s string, width int) string { return ansi.Truncate(s, max(1, width), "…") }
func (f *Form) buttons() [][2]string {
	switch f.stage {
	case "edit":
		if f.DraftPopupActive() && f.width < 48 {
			label := "More ^O"
			if f.advanced {
				label = "Less ^O"
			}
			return [][2]string{{"review", "Review ^S"}, {"advanced", label}, {"cancel", "Esc"}}
		}
		label := "Advanced ^O"
		if f.advanced {
			label = "Basic ^O"
		}
		return [][2]string{{"review", "Review ^S"}, {"advanced", label}, {"cancel", "Cancel Esc"}}
	case "review":
		return [][2]string{{"apply", "Apply ^S"}, {"back", "Back Esc"}, {"cancel", "Cancel"}}
	case "result":
		return [][2]string{{"return", "Return Enter"}}
	default:
		return [][2]string{{"cancel", "Cancel Esc"}}
	}
}
func (f *Form) hits() []hitRegion {
	if f.reviewModal() {
		return f.modalLayout().hits
	}
	if f.height < 6 {
		return nil
	}
	var hits []hitRegion
	if f.stage == "edit" {
		list, start, rows := f.fieldWindow()
		for n, i := range list[start:min(len(list), start+rows)] {
			y := 2 + n*2
			field := f.spec.Fields[i]
			if len(field.Options) > 0 {
				hits = append(hits, hitRegion{fmt.Sprintf("prev:%d", i), rect{2, y + 1, 2, 1}}, hitRegion{fmt.Sprintf("next:%d", i), rect{5 + ansi.StringWidth(safe(field.displayValue(f.inputs[i].Value()))), y + 1, 1, 1}})
			}
			if field.Kind == "schedule" {
				hits = append(hits, hitRegion{fmt.Sprintf("schedule:%d", i), rect{2, y + 1, max(0, f.width-4), 1}})
			}
			if field.Kind == "multiline" {
				hits = append(hits, hitRegion{fmt.Sprintf("multiline:%d", i), rect{2, y + 1, max(0, f.width-4), 1}})
			}
			if field.Pick != nil {
				hits = append(hits, hitRegion{fmt.Sprintf("browse:%d", i), rect{max(2, f.width-12), y, 10, 1}})
			}
			hits = append(hits, hitRegion{fmt.Sprintf("field:%d", i), rect{0, y, f.width, 2}})
		}
	}
	x := 0
	for _, b := range f.buttons() {
		w := ansi.StringWidth(b[1]) + 4
		if x+w <= f.width {
			hits = append(hits, hitRegion{b[0], rect{x, max(0, f.height-2), w, 1}})
		}
		x += w + 1
	}
	visible := hits[:0]
	for _, h := range hits {
		if h.rect.x >= 0 && h.rect.y >= 0 && h.rect.x+h.rect.w <= f.width && h.rect.y+h.rect.h <= f.height {
			visible = append(visible, h)
		}
	}
	return visible
}
func (f *Form) View() tea.View {
	if f.help != nil {
		return f.help.View()
	}
	if f.schedule != nil {
		return f.schedule.View()
	}
	if f.picker != nil {
		return f.pickerView()
	}
	if f.multiline != nil {
		return f.multilineView()
	}
	if f.reviewModal() {
		v := tea.NewView(f.modalView())
		v.AltScreen = true
		if f.spec.Mouse {
			v.MouseMode = tea.MouseModeCellMotion
		}
		return v
	}
	t := styles(f.dark)
	lines := []string{t.Title.Render(clip(f.spec.Title, f.width)), t.Muted.Render(clip("Tab next  ·  Shift+Tab back  ·  Ctrl+P browse", f.width))}
	switch f.stage {
	case "edit":
		list, start, rows := f.fieldWindow()
		for _, i := range list[start:min(len(list), start+rows)] {
			field := f.spec.Fields[i]
			prefix := "  "
			label := safe(field.Label)
			if i == f.focus {
				prefix = "› "
				label = t.Accent.Bold(true).Render(label)
			}
			line := prefix + label
			if field.Pick != nil && f.width >= 20 {
				line = pad(line, f.width-12) + t.Accent.Render("[Browse]")
			}
			lines = append(lines, clip(line, f.width))
			value := f.inputs[i].View()
			if len(field.Options) > 0 {
				value = t.Accent.Render("◀ ") + safe(field.displayValue(f.inputs[i].Value())) + t.Accent.Render(" ▶")
			}
			if field.Kind == "schedule" {
				value = t.Schedule.Render(safe(f.inputs[i].Value())) + t.Muted.Render("  [Edit schedule ↵]")
			}
			if field.Kind == "multiline" {
				body := f.fieldValue(i)
				summary := "Empty · Enter to write"
				if body != "" {
					summary = fmt.Sprintf("%d lines · Enter to edit", strings.Count(body, "\n")+1)
				}
				value = t.Schedule.Render(summary)
			}
			lines = append(lines, "  "+clip(value, max(1, f.width-3)))
		}
		hint := f.live
		if len(f.inputs) > 0 && f.spec.Fields[f.focus].Hint != "" {
			hint = f.spec.Fields[f.focus].Hint
		}
		if hint != "" {
			lines = append(lines, "", t.Muted.Render(clip(safe(hint), f.width)))
		}
	case "review":
		body := strings.Split(ansi.Hardwrap(safe(f.review.Text), max(1, f.width-2), true), "\n")
		start := min(f.scroll, max(0, len(body)-1))
		lines = append(lines, body[start:min(len(body), start+max(1, f.height-7))]...)
	case "building":
		lines = append(lines, t.Warning.Render("Preparing review…"))
	case "applying":
		label := "Applying to the reviewed target…"
		if f.cancelRequested {
			label = "Stopping; waiting for the dispatched operation's result…"
		}
		lines = append(lines, t.Warning.Render(label), "Cancellation cannot undo an already dispatched write.")
	case "result":
		body := strings.Split(ansi.Hardwrap(safe(f.message), max(1, f.width-2), true), "\n")
		start := min(f.scroll, max(0, len(body)-1))
		lines = append(lines, body[start:min(len(body), start+max(1, f.height-7))]...)
	}
	lines = lines[:min(len(lines), max(0, f.height-4))]
	for len(lines) < f.height-4 {
		lines = append(lines, "")
	}
	status := ""
	if f.err != nil {
		status = t.Error.Render("! " + safe(f.err.Error()))
	} else if f.stage == "review" {
		status = t.Muted.Render("Review target and changes. Enter does not apply.")
	} else if f.stage == "edit" {
		status = t.Muted.Render("Draft only · Ctrl+S reviews before saving")
		if f.DraftPopupActive() {
			visible, start, rows := f.fieldWindow()
			status = t.Muted.Render(fmt.Sprintf("Draft only · %d–%d/%d · Tab/↑↓/wheel", min(start+1, len(visible)), min(start+rows, len(visible)), len(visible)))
		}
	}
	lines = append(lines, clip(status, f.width), "")
	buttons := []string{}
	visibleButtons := map[string]bool{}
	for _, hit := range f.hits() {
		visibleButtons[hit.id] = true
	}
	for _, b := range f.buttons() {
		if visibleButtons[b[0]] {
			buttons = append(buttons, t.Accent.Render("[ "+b[1]+" ]"))
		}
	}
	lines = append(lines, clip(strings.Join(buttons, " "), f.width), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), f.width, f.height))
	v.AltScreen = true
	if f.spec.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}
func RunForm(ctx context.Context, spec FormSpec) (FormResult, error) {
	f := NewForm(ctx, spec)
	defer f.cancel()
	m, err := tea.NewProgram(f, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if err != nil {
		return FormResult{}, err
	}
	result := m.(*Form)
	if result.err != nil {
		return result.result, result.err
	}
	if !result.result.Submitted {
		return result.result, ErrCancelled
	}
	return result.result, nil
}
func Confirm(ctx context.Context, title, body string, apply func(context.Context) (string, error)) (FormResult, error) {
	return RunFormReview(ctx, FormSpec{Title: title, Build: func(context.Context, map[string]string) (Review, error) { return Review{Text: body}, nil }, Apply: func(ctx context.Context, _ map[string]string, _ Review) (string, error) { return apply(ctx) }})
}
func RunFormReview(ctx context.Context, spec FormSpec) (FormResult, error) {
	f := NewForm(ctx, spec)
	defer f.cancel()
	r, e := spec.Build(ctx, map[string]string{})
	if e != nil {
		return FormResult{}, e
	}
	f.review = r
	f.stage = "review"
	m, e := tea.NewProgram(f, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if e != nil {
		return FormResult{}, e
	}
	end := m.(*Form)
	if end.err != nil {
		return end.result, end.err
	}
	if !end.result.Submitted {
		return end.result, ErrCancelled
	}
	return end.result, nil
}
func ValueInt(v map[string]string, k string) (int, error) {
	var n int
	_, err := fmt.Sscanf(v[k], "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", k)
	}
	return n, nil
}
