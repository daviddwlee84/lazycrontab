package cli

import (
	"fmt"
	"strings"

	concept "github.com/daviddwlee84/lazycrontab/internal/help"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
)

func addConceptHelp(root *cobra.Command, o *options) {
	root.SetHelpCommand(&cobra.Command{Use: "help [topic or command]", Short: "Command help and practical cron concepts", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if topic, ok := concept.Find(args[0]); ok {
				return o.emit(cmd, topic, topic.Body)
			}
		}
		if len(args) > 0 && !(len(args) == 1 && args[0] == "topics") {
			target, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 || target == root {
				return usage("unknown help topic or command %q", strings.Join(args, " "))
			}
			return target.Help()
		}
		if o.interactive {
			_, err := ui.RunModel(cmd.Context(), ui.NewHelpBrowser("", true))
			return err
		}
		lines := []string{"Cron concepts · lazycrontab help TOPIC", ""}
		for _, topic := range concept.Topics() {
			lines = append(lines, fmt.Sprintf("  %-23s %s", topic.Name, topic.Summary))
		}
		lines = append(lines, "", "Use lazycrontab COMMAND --help for flags. In the TUI, F1 opens concepts.")
		return o.emit(cmd, concept.Topics(), strings.Join(lines, "\n"))
	}})
}
