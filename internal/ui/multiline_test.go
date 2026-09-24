package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestMultilineDraftPreservesBodyAndDoesNotSubmit(t *testing.T) {
	body := "echo first\nprintf '%s\\n' \"quoted\"\n"
	built := 0
	f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "script_content", Kind: "multiline", Label: "Script content", Value: body}}, Build: func(context.Context, map[string]string) (Review, error) { built++; return Review{}, nil }})
	defer f.cancel()
	if f.Values()["script_content"] != body {
		t.Fatal("one-line input sanitized initial body")
	}
	f.Update(special(tea.KeyEnter))
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	f.Update(tea.PasteMsg{Content: "echo \"hi\"\necho q"})
	f.Update(special(tea.KeyEnter))
	f.Update(key('q'))
	expected := body + "echo \"hi\"\necho q\nq"
	if f.Values()["script_content"] != expected || built != 0 {
		t.Fatal(f.Values(), built)
	}
	f.Update(ctrl('s'))
	if f.multiline != nil || f.stage != "edit" || built != 0 {
		t.Fatal("Done submitted instead of returning to draft")
	}
	f.Update(special(tea.KeyEnter))
	f.Update(special(tea.KeyEscape))
	if f.Values()["script_content"] != expected {
		t.Fatal("Back lost content")
	}
}

func TestMultilineDoesNotSilentlyRewriteTabsOrLineEndings(t *testing.T) {
	for _, body := range []string{"cat <<-EOF\n\tindented\nEOF\n", "echo a\r\necho b\r\n"} {
		f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "body", Kind: "multiline", Value: body}}})
		f.openMultiline()
		if !f.multiline.readonly {
			t.Fatal("lossy editor was allowed")
		}
		f.Update(key('x'))
		f.Update(ctrl('s'))
		if f.Values()["body"] != body {
			t.Fatal("body was normalized")
		}
		f.cancel()
	}
	f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "body", Kind: "multiline", Value: "echo hi"}}})
	defer f.cancel()
	f.openMultiline()
	f.Update(tea.PasteMsg{Content: "\twith tab"})
	if f.Values()["body"] != "echo hi" || !strings.Contains(f.multiline.notice, "F4") {
		t.Fatal("paste changed shell-significant tabs")
	}
}

func TestMultilineExternalResultOwnershipAndSmallLayout(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Mouse: true, Fields: []Field{{Key: "body", Label: "Script content", Kind: "multiline", Value: "echo hi"}}})
	defer f.cancel()
	f.openMultiline()
	f.multiline.editGeneration = 10
	f.Update(textEditorResult{owner: f, generation: 9, content: "stale"})
	if f.Values()["body"] != "echo hi" {
		t.Fatal("stale editor result replaced content")
	}
	f.Update(textEditorResult{owner: f, generation: 10, content: "echo hi\n\t# exact tabs\n"})
	if f.Values()["body"] != "echo hi\n\t# exact tabs\n" {
		t.Fatal("external content was rewritten")
	}
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		f.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(f.View().Content, "\n")
		if len(lines) > size[1] {
			t.Fatal("height overflow")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("width overflow")
			}
		}
		for _, hit := range f.multilineHits() {
			if hit.rect.y >= len(lines) || !strings.Contains(ansi.Strip(lines[hit.rect.y]), "Done") {
				t.Fatal("invisible editor action")
			}
		}
	}
}
