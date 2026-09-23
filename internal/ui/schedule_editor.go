package ui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

// ScheduleUseMsg transfers a valid expression into a job draft; it never writes a source.
type ScheduleUseMsg struct {
	Expression string
	Dialect    schedule.Dialect
	Timezone   string
}

type ScheduleEditorOptions struct {
	Expression string
	Dialect    schedule.Dialect
	Timezone   string
	Locale     string
	Theme      string
	Mouse      bool
	Target     string
	// OnUse mounts a shared job workflow without discarding the playground draft.
	OnUse func(ScheduleUseMsg) (tea.Model, error)
}

type schedulePreviewMsg struct {
	owner, generation uint64
	description       string
	next              []time.Time
	warnings          []string
	err               error
}

type scheduleHit struct {
	x, y, w, h int
	id         string
}

type scheduleLayout struct {
	lines []string
	hits  []scheduleHit
}

type scheduleChoice struct{ name, expression string }

var schedulePresets = []scheduleChoice{
	{"Every minute", "* * * * *"}, {"Every 5 minutes", "*/5 * * * *"},
	{"Every 15 minutes", "*/15 * * * *"}, {"Hourly", "0 * * * *"},
	{"Daily at 09:00", "0 9 * * *"}, {"Weekdays at 09:00", "0 9 * * 1-5"},
	{"Monday at 09:00", "0 9 * * 1"}, {"First of month", "0 9 1 * *"},
}
var scheduleMacros = []scheduleChoice{
	{"Every hour", "@hourly"}, {"Every day", "@daily"}, {"Every week", "@weekly"},
	{"Every month", "@monthly"}, {"Every year", "@yearly"}, {"Daemon startup", "@reboot"},
}
var editorSequence atomic.Uint64

// ScheduleEditor is shared by the dashboard, job forms and standalone playground.
// Its View has no terminal ownership: the containing model owns the alternate screen.
type ScheduleEditor struct {
	ctx                           context.Context
	options                       ScheduleEditorOptions
	owner, generation             uint64
	width, height, mode, focus    int
	preset, macro                 int
	inputs                        []textinput.Model
	phrase                        textinput.Model
	editing, valid, pending, dark bool
	expression, diagnostic        string
	description, status, pressed  string
	next                          []time.Time
	warnings                      []string
	cancelPreview                 context.CancelFunc
}

func NewScheduleEditor(ctx context.Context, options ScheduleEditorOptions) *ScheduleEditor {
	if options.Dialect == "" {
		options.Dialect = schedule.System
	}
	if options.Locale == "" {
		options.Locale = "en"
	}
	if options.Expression == "" {
		options.Expression = "0 9 * * 1-5"
	}
	e := &ScheduleEditor{ctx: ctx, options: options, owner: editorSequence.Add(1), width: 80, height: 22, dark: true}
	e.phrase = scheduleInput("every weekday at 09:00")
	e.setExpression(options.Expression)
	return e
}

func scheduleInput(value string) textinput.Model {
	i := textinput.New()
	i.Prompt = ""
	i.SetVirtualCursor(true)
	i.CharLimit = 512
	i.SetValue(value)
	return i
}

func (e *ScheduleEditor) Init() tea.Cmd      { return tea.Batch(e.preview(), tea.RequestBackgroundColor) }
func (e *ScheduleEditor) Expression() string { return e.expression }
func (e *ScheduleEditor) Valid() bool        { return e.valid && !e.pending }
func (e *ScheduleEditor) Editing() bool      { return e.editing }
func (e *ScheduleEditor) Blur() {
	e.editing = false
	e.phrase.Blur()
	for i := range e.inputs {
		e.inputs[i].Blur()
	}
}
func (e *ScheduleEditor) SetSize(width, height int) {
	e.width, e.height = max(1, width), max(1, height)
	e.pressed = ""
	e.phrase.SetWidth(max(1, width-4))
}
func (e *ScheduleEditor) SetMouse(enabled bool) { e.options.Mouse = enabled; e.pressed = "" }
func (e *ScheduleEditor) SetContext(dialect schedule.Dialect, timezone, locale string) tea.Cmd {
	if dialect == "" {
		dialect = schedule.System
	}
	if locale == "" {
		locale = "en"
	}
	if dialect == e.options.Dialect && timezone == e.options.Timezone && locale == e.options.Locale {
		return nil
	}
	e.options.Dialect, e.options.Timezone, e.options.Locale = dialect, timezone, locale
	e.pressed = ""
	return e.preview()
}
func (e *ScheduleEditor) SetExpression(expression string) tea.Cmd {
	e.Blur()
	e.setExpression(expression)
	return e.preview()
}

func (e *ScheduleEditor) setExpression(expression string) {
	e.expression = strings.TrimSpace(expression)
	if strings.HasPrefix(e.expression, "@") {
		e.mode = 3
		for i, c := range scheduleMacros {
			if c.expression == e.expression {
				e.macro = i
			}
		}
		if len(e.inputs) == 0 {
			e.setFields(strings.Fields("0 9 * * 1-5"))
		}
		return
	}
	e.mode = 0
	fields := strings.Fields(e.expression)
	if len(fields) == 0 {
		fields = []string{"", "*", "*", "*", "*"}
	}
	e.setFields(fields)
	e.expression = e.fieldsExpression()
}
func (e *ScheduleEditor) setFields(fields []string) {
	e.inputs = nil
	for _, v := range fields {
		e.inputs = append(e.inputs, scheduleInput(v))
	}
	e.focus = min(e.focus, max(0, len(e.inputs)-1))
}
func (e *ScheduleEditor) fieldsExpression() string {
	fields := make([]string, len(e.inputs))
	for i := range e.inputs {
		fields[i] = e.inputs[i].Value()
	}
	return strings.Join(fields, " ")
}
func (e *ScheduleEditor) fieldNames() []string {
	names := []string{"Minute", "Hour", "Day of month", "Month", "Weekday"}
	if len(e.inputs) == 6 {
		names = append(names, "Year")
	}
	if len(e.inputs) == 7 {
		names = append([]string{"Second"}, append(names, "Year")...)
	}
	for len(names) < len(e.inputs) {
		names = append(names, "Extra field")
	}
	return names
}
func (e *ScheduleEditor) fieldHint() string {
	names := e.fieldNames()
	name := names[min(e.focus, len(names)-1)]
	rangeText := map[string]string{"Minute": "0–59", "Hour": "0–23", "Day of month": "1–31", "Month": "1–12 or JAN–DEC", "Weekday": "0–7 or SUN–SAT", "Second": "0–59", "Year": "1970–2099"}[name]
	return name + " · " + rangeText + " · * any · , list · - range · / step (*/5)"
}

func (e *ScheduleEditor) draftDiagnostic() string {
	if e.mode != 0 {
		return ""
	}
	names := e.fieldNames()
	for i, input := range e.inputs {
		v := input.Value()
		if v == "" {
			return names[i] + ": incomplete — enter a value or *"
		}
		if strings.IndexFunc(v, unicode.IsSpace) >= 0 {
			return names[i] + ": each box contains one field; paste a complete expression to fill all boxes"
		}
		if strings.HasSuffix(v, "/") || strings.HasSuffix(v, "-") || strings.HasSuffix(v, ",") || strings.HasSuffix(v, "#") {
			return names[i] + ": incomplete — finish the value after " + v[len(v)-1:]
		}
		for n := 0; n+1 < len(v); n++ {
			if v[n] == '*' && v[n+1] >= '0' && v[n+1] <= '9' {
				return names[i] + ": missing / after *; use */5, not *5"
			}
		}
	}
	return ""
}
func (e *ScheduleEditor) preview() tea.Cmd {
	if e.cancelPreview != nil {
		e.cancelPreview()
	}
	e.generation++
	e.status = ""
	e.valid, e.pending = false, false
	e.description, e.next, e.warnings = "", nil, nil
	e.diagnostic = e.draftDiagnostic()
	if e.diagnostic != "" {
		return nil
	}
	e.pending = true
	ctx, cancel := context.WithCancel(e.ctx)
	e.cancelPreview = cancel
	owner, generation, expression, options := e.owner, e.generation, e.expression, e.options
	names := append([]string(nil), e.fieldNames()...)
	return func() tea.Msg {
		msg := schedulePreviewMsg{owner: owner, generation: generation}
		zone := options.Timezone
		if zone == "" {
			zone = "UTC"
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			msg.err = err
			return msg
		}
		sc, err := schedule.Parse(expression, options.Dialect, loc, options.Locale)
		if err != nil {
			fields := strings.Fields(expression)
			if len(fields) == len(names) && !strings.HasPrefix(expression, "@") {
				for i := range fields {
					probe := make([]string, len(fields))
					for j := range probe {
						probe[j] = "*"
					}
					probe[i] = fields[i]
					if _, fieldErr := schedule.Parse(strings.Join(probe, " "), options.Dialect, loc, options.Locale); fieldErr != nil {
						err = fmt.Errorf("%s: %w", names[i], fieldErr)
						break
					}
				}
			}
			msg.err = err
			return msg
		}
		msg.description, msg.warnings = sc.Description, sc.Warnings
		if options.Timezone == "" {
			msg.warnings = append(msg.warnings, "Timezone unknown; upcoming times unavailable. Choose a target timezone to preview.")
			return msg
		}
		msg.next, msg.err = sc.NextN(ctx, time.Now().In(loc), 5)
		if msg.err == nil && len(msg.next) == 0 && !sc.Event {
			msg.warnings = append(msg.warnings, "No occurrence found within the parser search horizon.")
		}
		return msg
	}
}

func (e *ScheduleEditor) changeMode(mode int) tea.Cmd {
	e.Blur()
	e.mode = mode
	e.pressed = ""
	if mode == 0 {
		e.expression = e.fieldsExpression()
	}
	if mode == 1 {
		return e.choosePreset(e.preset)
	}
	if mode == 2 {
		return e.updatePhrase()
	}
	if mode == 3 {
		return e.chooseMacro(e.macro)
	}
	return e.preview()
}
func (e *ScheduleEditor) choosePreset(index int) tea.Cmd {
	e.preset = max(0, min(len(schedulePresets)-1, index))
	e.expression = schedulePresets[e.preset].expression
	e.setFields(strings.Fields(e.expression))
	return e.preview()
}
func (e *ScheduleEditor) chooseMacro(index int) tea.Cmd {
	e.macro = max(0, min(len(scheduleMacros)-1, index))
	e.expression = scheduleMacros[e.macro].expression
	return e.preview()
}
func (e *ScheduleEditor) updatePhrase() tea.Cmd {
	x, err := schedule.FromHuman(e.phrase.Value())
	if err != nil {
		e.generation++
		e.expression = ""
		e.valid, e.pending = false, false
		e.diagnostic = err.Error()
		e.description = ""
		e.next = nil
		return nil
	}
	e.expression = x
	e.setFields(strings.Fields(x))
	return e.preview()
}
func (e *ScheduleEditor) moveField(delta int) {
	e.Blur()
	e.focus = (e.focus + delta + len(e.inputs)) % len(e.inputs)
}
func (e *ScheduleEditor) beginEdit() tea.Cmd {
	if e.mode != 0 && e.mode != 2 {
		return nil
	}
	e.editing = true
	if e.mode == 2 {
		return e.phrase.Focus()
	}
	return e.inputs[e.focus].Focus()
}
func (e *ScheduleEditor) use() tea.Cmd {
	if !e.Valid() {
		e.status = "Finish a valid expression before creating a job."
		return nil
	}
	msg := ScheduleUseMsg{e.expression, e.options.Dialect, e.options.Timezone}
	return func() tea.Msg { return msg }
}
func (e *ScheduleEditor) copy() tea.Cmd {
	if !e.Valid() {
		e.status = "Finish a valid expression before copying."
		return nil
	}
	e.status = "Copy requested through your terminal clipboard (OSC 52)."
	return tea.SetClipboard(e.expression)
}
func (e *ScheduleEditor) setFieldCount(count int) tea.Cmd {
	if e.options.Dialect != schedule.Supercronic && count != 5 {
		return nil
	}
	e.Blur()
	fields := strings.Fields(e.fieldsExpression())
	if len(fields) == 7 {
		fields = fields[1:]
	}
	if len(fields) == 6 {
		fields = fields[:5]
	}
	for len(fields) < 5 {
		fields = append(fields, "*")
	}
	fields = fields[:5]
	if count >= 6 {
		fields = append(fields, "*")
	}
	if count == 7 {
		fields = append([]string{"0"}, fields...)
	}
	e.setFields(fields)
	e.expression = e.fieldsExpression()
	e.mode = 0
	return e.preview()
}
func (e *ScheduleEditor) activate(id string) tea.Cmd {
	switch id {
	case "copy":
		return e.copy()
	case "use":
		return e.use()
	case "mode0":
		return e.changeMode(0)
	case "mode1":
		return e.changeMode(1)
	case "mode2":
		return e.changeMode(2)
	case "mode3":
		return e.changeMode(3)
	case "count5":
		return e.setFieldCount(5)
	case "count6":
		return e.setFieldCount(6)
	case "count7":
		return e.setFieldCount(7)
	case "phrase":
		e.Blur()
		return e.beginEdit()
	}
	var index int
	if _, err := fmt.Sscanf(id, "field%d", &index); err == nil && index >= 0 && index < len(e.inputs) {
		e.Blur()
		e.focus = index
		return e.beginEdit()
	}
	if _, err := fmt.Sscanf(id, "preset%d", &index); err == nil {
		return e.choosePreset(index)
	}
	if _, err := fmt.Sscanf(id, "macro%d", &index); err == nil {
		return e.chooseMacro(index)
	}
	return nil
}

func (e *ScheduleEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case schedulePreviewMsg:
		if v.owner != e.owner || v.generation != e.generation {
			return e, nil
		}
		e.pending = false
		e.valid = v.err == nil
		if v.err != nil {
			e.diagnostic = v.err.Error()
		} else {
			e.diagnostic = ""
			e.description, e.next, e.warnings = v.description, v.next, v.warnings
		}
		return e, nil
	case tea.WindowSizeMsg:
		e.SetSize(v.Width, v.Height)
		return e, nil
	case tea.BackgroundColorMsg:
		e.dark = v.IsDark()
		return e, nil
	case tea.MouseClickMsg:
		if !e.options.Mouse {
			return e, nil
		}
		m := v.Mouse()
		e.pressed = ""
		if m.Button != tea.MouseLeft {
			return e, nil
		}
		for _, h := range e.layout().hits {
			if m.X >= h.x && m.X < h.x+h.w && m.Y >= h.y && m.Y < h.y+h.h {
				e.pressed = h.id
				break
			}
		}
		return e, nil
	case tea.MouseReleaseMsg:
		if !e.options.Mouse {
			return e, nil
		}
		id := e.pressed
		e.pressed = ""
		m := v.Mouse()
		for _, h := range e.layout().hits {
			if id != "" && h.id == id && m.X >= h.x && m.X < h.x+h.w && m.Y >= h.y && m.Y < h.y+h.h {
				return e, e.activate(id)
			}
		}
		return e, nil
	case tea.MouseWheelMsg:
		if !e.options.Mouse {
			return e, nil
		}
		e.pressed = ""
		d := 1
		if strings.Contains(v.String(), "up") {
			d = -1
		}
		if e.mode == 1 {
			return e, e.choosePreset(e.preset + d)
		}
		if e.mode == 3 {
			return e, e.chooseMacro(e.macro + d)
		}
		e.moveField(d)
		return e, nil
	case tea.PasteMsg:
		if !e.editing {
			return e, nil
		}
		if e.mode == 0 {
			fields := strings.Fields(v.Content)
			if len(fields) == 1 && strings.HasPrefix(fields[0], "@") {
				e.Blur()
				e.setExpression(fields[0])
				return e, e.preview()
			}
			if len(fields) > 1 {
				if len(fields) != len(e.inputs) {
					e.status = fmt.Sprintf("Paste has %d fields; this editor expects %d. Choose the matching field layout first.", len(fields), len(e.inputs))
					return e, nil
				}
				e.setFields(fields)
				e.inputs[e.focus].Focus()
				e.expression = e.fieldsExpression()
				return e, e.preview()
			}
		}
	case tea.KeyPressMsg:
		e.pressed = ""
		key := v.String()
		switch key {
		case "f1":
			return e, e.changeMode(0)
		case "f2":
			return e, e.changeMode(1)
		case "f3":
			return e, e.changeMode(2)
		case "f4":
			return e, e.changeMode(3)
		case "ctrl+y":
			return e, e.copy()
		case "ctrl+s":
			if !v.IsRepeat {
				return e, e.use()
			}
			return e, nil
		case "esc":
			e.Blur()
			return e, nil
		case "tab", "shift+tab":
			d := 1
			if key == "shift+tab" {
				d = -1
			}
			wasEditing := e.editing
			e.moveField(d)
			if wasEditing {
				return e, e.beginEdit()
			}
			return e, nil
		}
		if !e.editing {
			switch key {
			case "enter", "i":
				return e, e.beginEdit()
			case "c":
				return e, e.copy()
			case "u":
				return e, e.use()
			case "left", "h":
				e.moveField(-1)
			case "right", "l":
				e.moveField(1)
			case "up", "k":
				if e.mode == 1 {
					return e, e.choosePreset(e.preset - 1)
				}
				if e.mode == 3 {
					return e, e.chooseMacro(e.macro - 1)
				}
				e.moveField(-1)
			case "down", "j":
				if e.mode == 1 {
					return e, e.choosePreset(e.preset + 1)
				}
				if e.mode == 3 {
					return e, e.chooseMacro(e.macro + 1)
				}
				e.moveField(1)
			case "5", "6", "7":
				if e.mode == 0 && e.options.Dialect == schedule.Supercronic {
					return e, e.setFieldCount(int(key[0] - '0'))
				}
			}
			return e, nil
		}
		if key == "enter" {
			e.Blur()
			return e, nil
		}
		if key == "space" && e.mode == 0 {
			e.moveField(1)
			return e, e.beginEdit()
		}
	}
	if e.editing {
		if e.mode == 2 {
			before := e.phrase.Value()
			var cmd tea.Cmd
			e.phrase, cmd = e.phrase.Update(msg)
			if before != e.phrase.Value() {
				return e, tea.Batch(cmd, e.updatePhrase())
			}
			return e, cmd
		}
		before := e.inputs[e.focus].Value()
		var cmd tea.Cmd
		e.inputs[e.focus], cmd = e.inputs[e.focus].Update(msg)
		if before != e.inputs[e.focus].Value() {
			e.expression = e.fieldsExpression()
			e.status = ""
			return e, tea.Batch(cmd, e.preview())
		}
		return e, cmd
	}
	return e, nil
}

func (e *ScheduleEditor) paint(s, role string, bold bool) string {
	dark := e.dark
	if e.options.Theme == "dark" {
		dark = true
	}
	if e.options.Theme == "light" {
		dark = false
	}
	t := styles(dark)
	style := t.Muted
	switch role {
	case "accent":
		style = t.Accent
	case "schedule":
		style = t.Schedule
	case "success":
		style = t.Success
	case "error":
		style = t.Error
	case "warning":
		style = t.Warning
	}
	return style.Bold(bold).Render(s)
}
func (e *ScheduleEditor) layout() scheduleLayout {
	l := scheduleLayout{}
	add := func(line string) { l.lines = append(l.lines, clip(line, e.width)) }
	button := func(id, label string, active bool, x, y int) string {
		s := "[" + label + "]"
		w := ansi.StringWidth(s)
		if x+w <= e.width && y < max(0, e.height-2) {
			l.hits = append(l.hits, scheduleHit{x, y, w, 1, id})
		}
		if active {
			return e.paint(s, "accent", true)
		}
		return s
	}
	x, modes := 0, []string{"F1 Fields", "F2 Presets", "F3 English", "F4 Macros"}
	line := ""
	for i, name := range modes {
		if e.width < 50 {
			name = []string{"F1 Cron", "F2 Pick", "F3 Text", "F4 @"}[i]
		}
		p := button(fmt.Sprintf("mode%d", i), name, e.mode == i, x, 0)
		line += p + " "
		x += ansi.StringWidth(p) + 1
	}
	add(line)
	context := string(e.options.Dialect) + " · " + e.options.Timezone
	if e.options.Timezone == "" {
		context = string(e.options.Dialect) + " · timezone unknown"
	}
	if e.options.Target != "" {
		context += " · new job → " + e.options.Target
	}
	add(e.paint(context, "muted", false))
	switch e.mode {
	case 0:
		if e.options.Dialect == schedule.Supercronic {
			x, line = 0, ""
			for _, n := range []int{5, 6, 7} {
				label := fmt.Sprint(n) + " fields"
				if n == 6 {
					label = "6 +year"
				}
				if n == 7 {
					label = "7 sec+year"
				}
				p := button(fmt.Sprintf("count%d", n), label, len(e.inputs) == n, x, len(l.lines))
				line += p + " "
				x += ansi.StringWidth(p) + 1
			}
			add(line)
		}
		names := e.fieldNames()
		start, end := 0, len(e.inputs)
		if e.width < 60 || e.height < 18 {
			start, end = e.focus, e.focus+1
			add(fmt.Sprintf("Field %d/%d · Tab / Shift+Tab to move", e.focus+1, len(e.inputs)))
		}
		x, y := 0, len(l.lines)
		labels, values, bottoms := "", "", ""
		boxed := e.width >= 60 && e.height >= 18
		flush := func() {
			if x > 0 {
				add(labels)
				add(values)
				if boxed {
					add(bottoms)
				}
				x = 0
				labels, values, bottoms = "", "", ""
				y = len(l.lines)
			}
		}
		for i := start; i < end; i++ {
			w := min(e.width, max(13, min(28, max(ansi.StringWidth(names[i])+4, ansi.StringWidth(e.inputs[i].Value())+4))))
			if x+w > e.width {
				flush()
			}
			label := clip(names[i], max(1, w-1))
			value := safe(e.inputs[i].Value())
			if e.editing && i == e.focus {
				input := e.inputs[i]
				input.SetWidth(max(1, w-4))
				value = input.View()
			}
			if boxed {
				label = clip(label, max(1, w-4))
				label = "╭ " + label + " " + strings.Repeat("─", max(0, w-4-ansi.StringWidth(label))) + "╮"
				value = "│ " + pad(value, max(1, w-4)) + " │"
				edge := "muted"
				if i == e.focus {
					edge = "accent"
				}
				bottoms += e.paint("╰"+strings.Repeat("─", max(0, w-2))+"╯", edge, false)
			} else {
				value = "[" + clip(value, max(1, w-3)) + "]"
			}
			if i == e.focus {
				if !boxed {
					label = "› " + label
				}
				label = e.paint(label, "accent", true)
				value = e.paint(value, "schedule", true)
			}
			labels += clip(label, w) + strings.Repeat(" ", max(0, w-ansi.StringWidth(label)))
			values += value + strings.Repeat(" ", max(0, w-ansi.StringWidth(value)))
			hitHeight := 2
			if boxed {
				hitHeight = 3
			}
			if y+hitHeight <= max(0, e.height-2) {
				l.hits = append(l.hits, scheduleHit{x, y, w, hitHeight, fmt.Sprintf("field%d", i)})
			}
			x += w
		}
		flush()
		add(e.paint(e.fieldHint(), "muted", false))
	case 1, 3:
		choices, selected, prefix := schedulePresets, e.preset, "preset"
		if e.mode == 3 {
			choices, selected, prefix = scheduleMacros, e.macro, "macro"
		}
		limit := max(1, min(5, e.height-9))
		start := max(0, selected-limit+1)
		for i := start; i < min(len(choices), start+limit); i++ {
			marker := "  "
			if i == selected {
				marker = "› "
			}
			label := marker + choices[i].name + "  " + choices[i].expression
			if i == selected {
				label = e.paint(label, "schedule", true)
			}
			y := len(l.lines)
			add(label)
			if y < max(0, e.height-2) {
				l.hits = append(l.hits, scheduleHit{0, y, e.width, 1, fmt.Sprintf("%s%d", prefix, i)})
			}
		}
	case 2:
		add("English phrase · Enter to edit")
		value := e.phrase.Value()
		if e.editing {
			value = e.phrase.View()
		}
		y := len(l.lines)
		add("[ " + value + " ]")
		if y < max(0, e.height-2) {
			l.hits = append(l.hits, scheduleHit{0, y, e.width, 1, "phrase"})
		}
		add("Try: every 15 minutes · daily at 03:00")
	}
	add("")
	add(e.paint(safe(e.expression), "schedule", true))
	if e.pending {
		add("Checking expression…")
	} else if e.diagnostic != "" {
		for _, s := range strings.Split(ansi.Hardwrap("! "+safe(e.diagnostic), max(1, e.width), true), "\n") {
			add(e.paint(s, "error", false))
		}
	} else {
		for _, s := range strings.Split(ansi.Hardwrap(safe(e.description), max(1, e.width), true), "\n") {
			add(e.paint(s, "success", true))
		}
		for _, t := range e.next {
			add("Next  " + t.Format("Mon 02 Jan 15:04:05 -07:00"))
		}
		for _, s := range e.warnings {
			add("! " + s)
		}
	}
	available := max(0, e.height-2)
	if len(l.lines) > available {
		l.lines = l.lines[:available]
	}
	for len(l.lines) < available {
		l.lines = append(l.lines, "")
	}
	// Actions have exact visible rectangles; clipped buttons are never clickable.
	y := len(l.lines)
	actionLine := ""
	x = 0
	for _, b := range []struct{ id, label string }{{"copy", "c Copy"}, {"use", "u Use in new job"}} {
		s := "[" + b.label + "]"
		w := ansi.StringWidth(s)
		if x+w <= e.width {
			l.hits = append(l.hits, scheduleHit{x, y, w, 1, b.id})
		}
		if e.Valid() {
			s = e.paint(s, "accent", true)
		}
		actionLine += s + " "
		x += w + 1
	}
	if e.height >= 2 {
		add(actionLine)
	}
	if e.height >= 1 {
		hint := "Tab field · Enter edit · Esc blur · F1–F4 modes"
		if e.mode == 1 || e.mode == 3 {
			hint = "↑↓ choose · c copy · u new job · F1–F4 modes"
		}
		if e.mode == 2 {
			hint = "Enter edit phrase · c copy · u new job · F1–F4 modes"
		}
		if e.editing {
			hint = "Typing · Tab/Space next · Esc blur · Ctrl+Y copy · Ctrl+S use"
		}
		if e.diagnostic != "" && e.height < 18 {
			hint = "! " + e.diagnostic
		}
		if e.status != "" {
			hint = e.status
		}
		add(hint)
	}
	if len(l.lines) > e.height {
		l.lines = l.lines[:e.height]
	}
	return l
}
func (e *ScheduleEditor) View() tea.View { return tea.NewView(strings.Join(e.layout().lines, "\n")) }

type standaloneSchedule struct {
	editor        *ScheduleEditor
	result        *ScheduleUseMsg
	cancelled     bool
	child         tea.Model
	width, height int
}

func (m *standaloneSchedule) Init() tea.Cmd { return m.editor.Init() }
func (m *standaloneSchedule) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.editor.SetSize(size.Width, max(1, size.Height-2))
		if m.child == nil {
			return m, nil
		}
	}
	if _, ok := msg.(schedulePreviewMsg); ok {
		_, cmd := m.editor.Update(msg)
		return m, cmd
	}
	if done, ok := msg.(WorkflowDoneMsg); ok && m.child != nil {
		if done.Owner != nil && done.Owner != m.child {
			return m, nil
		}
		m.child = nil
		m.editor.status = done.Message
		if done.Err != nil && done.Err != ErrCancelled {
			m.editor.status = done.Err.Error()
		}
		return m, nil
	}
	if m.child != nil {
		var cmd tea.Cmd
		m.child, cmd = m.child.Update(msg)
		return m, cmd
	}
	if use, ok := msg.(ScheduleUseMsg); ok {
		if m.editor.options.OnUse != nil {
			child, err := m.editor.options.OnUse(use)
			if err != nil {
				m.editor.status = err.Error()
				return m, nil
			}
			if child == nil {
				return m, nil
			}
			m.editor.Blur()
			m.child = embedded(child)
			if target, ok := m.child.(interface{ SetMouse(bool) }); ok {
				target.SetMouse(m.editor.options.Mouse)
			}
			_, sizeCmd := m.child.Update(tea.WindowSizeMsg{Width: max(1, m.width), Height: max(1, m.height)})
			return m, tea.Batch(m.child.Init(), sizeCmd)
		}
		m.result = &use
		return m, tea.Quit
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
		if !m.editor.Editing() && (key.String() == "q" || key.String() == "esc") {
			return m, tea.Quit
		}
	}
	switch mouse := msg.(type) {
	case tea.MouseClickMsg:
		if mouse.Y < 2 {
			return m, nil
		}
		mouse.Y -= 2
		msg = mouse
	case tea.MouseReleaseMsg:
		if mouse.Y < 2 {
			m.editor.pressed = ""
			return m, nil
		}
		mouse.Y -= 2
		msg = mouse
	case tea.MouseWheelMsg:
		if mouse.Y < 2 {
			return m, nil
		}
		mouse.Y -= 2
		msg = mouse
	}
	_, cmd := m.editor.Update(msg)
	return m, cmd
}
func (m *standaloneSchedule) View() tea.View {
	if m.child != nil {
		return m.child.View()
	}
	v := m.editor.View()
	v.Content = fitLines(m.editor.paint("Schedule playground · q exit", "accent", true)+"\n\n"+v.Content, max(1, m.width), max(1, m.height))
	v.AltScreen = true
	if m.editor.options.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}
func RunScheduleEditor(ctx context.Context, options ScheduleEditorOptions) (*ScheduleUseMsg, error) {
	m := &standaloneSchedule{editor: NewScheduleEditor(ctx, options), width: 80, height: 24}
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if m.editor.cancelPreview != nil {
		m.editor.cancelPreview()
	}
	if err != nil {
		return nil, err
	}
	if m.cancelled {
		return nil, ErrCancelled
	}
	return m.result, nil
}
