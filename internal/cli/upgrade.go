package cli

import (
	"context"
	"github.com/daviddwlee84/lazycrontab/internal/upgrade"
	"github.com/spf13/cobra"
)

func addUpgrade(root *cobra.Command, o *options) {
	var check bool
	cmd := &cobra.Command{Use: "upgrade", Short: "Check or update this installed executable", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		p, e := upgrade.Check(cmd.Context(), o.version)
		if e != nil {
			return e
		}
		if check || o.dry || !p.Supported {
			return o.emit(cmd, p, pretty(p))
		}
		return o.approve(cmd, "Upgrade lazycrontab", p, pretty(p), func(ctx context.Context) (any, string, error) {
			message, e := upgrade.Apply(ctx, p, cmd.ErrOrStderr())
			return map[string]string{"result": message}, message, e
		})
	}}
	cmd.Flags().BoolVar(&check, "check", false, "Read-only version and ownership check")
	root.AddCommand(cmd)
}
