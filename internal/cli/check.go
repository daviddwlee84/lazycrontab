package cli

import "github.com/spf13/cobra"

func addCheck(root *cobra.Command, o *options) {
	root.AddCommand(&cobra.Command{Use: "check ID", Short: "Inspect script paths and runtime without executing the job", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, snapshot, err := o.snapshot(cmd)
		if err != nil {
			return err
		}
		report, err := s.CheckJob(cmd.Context(), snapshot, args[0])
		if err != nil {
			return err
		}
		return o.emit(cmd, report, report.Text())
	}})
}
