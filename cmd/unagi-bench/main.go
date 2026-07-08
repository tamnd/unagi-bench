// Command unagi-bench benchmarks the unagi Python-to-Go compiler against CPython
// and, when present, PyPy. It builds each workload with unagi, runs it under every
// available engine, checks the engines agree with CPython's output, and reports
// the speedup against the campaign goal of 2x over the fastest competitor.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/tamnd/unagi-bench/pkg/bench"
)

// version is stamped by the release build; a dev build reports "dev".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx))
}

func run(ctx context.Context) int {
	root := newRoot()
	if err := fang.Execute(ctx, root, fang.WithVersion(version)); err != nil {
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "unagi-bench",
		Short:         "Benchmark unagi against CPython and PyPy",
		Long:          "unagi-bench times the unagi compiler's native binaries against CPython 3.14\nand optionally PyPy, verifying every engine agrees with CPython's output, and\nreports the speedup against the 2x goal.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd(), newReportCmd())
	return root
}

func newRunCmd() *cobra.Command {
	var (
		dir       string
		reps      int
		warmup    int
		only      string
		unagiPath string
		jsonOut   string
		gate      bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the benchmark matrix and report the speedups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := bench.Config{
				WorkloadsDir: dir,
				Reps:         reps,
				Warmup:       warmup,
				Only:         only,
				UnagiPath:    unagiPath,
				Log:          func(s string) { fmt.Fprintln(os.Stderr, s) },
			}
			res, err := bench.Run(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			bench.Render(cmd.OutOrStdout(), res)

			if jsonOut != "" {
				f, err := os.Create(jsonOut)
				if err != nil {
					return err
				}
				defer f.Close()
				if err := bench.Marshal(f, res); err != nil {
					return err
				}
			}
			if gate && !bench.Summarize(res).GoalMet {
				return fmt.Errorf("goal not met: unagi did not reach %.0fx over the fastest competitor", bench.Goal)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "workloads", "workloads", "Directory of .py workloads to benchmark")
	cmd.Flags().IntVar(&reps, "reps", 5, "Timed repetitions per engine")
	cmd.Flags().IntVar(&warmup, "warmup", 1, "Discarded warmup passes per engine")
	cmd.Flags().StringVar(&only, "only", "", "Only run workloads whose name contains this substring")
	cmd.Flags().StringVar(&unagiPath, "unagi", "", "Path to the unagi binary (defaults to PATH)")
	cmd.Flags().StringVar(&jsonOut, "json", "", "Write results.json to this path")
	cmd.Flags().BoolVar(&gate, "gate", false, "Exit non-zero when the 2x goal is not met")
	return cmd
}

func newReportCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Render a stored results.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer f.Close()
			res, err := bench.Parse(f)
			if err != nil {
				return err
			}
			bench.Render(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "results.json", "Results file to render")
	return cmd
}
