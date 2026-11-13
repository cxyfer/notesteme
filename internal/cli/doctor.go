package cli

import (
	"context"
	"fmt"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/doctor"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

func parseDoctor(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("doctor", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("doctor 不接受位置參數：%q。", pos[0])
	}
	// doctor 檢查的是這台機器的環境，不需要資料目錄或 server，所以不判斷模式。
	return &plan{g: g, noBackend: true, run: func(ctx context.Context, s *session) (int, error) {
		cfg, err := config.Load(config.Sources{Env: s.env.Vars})
		if err != nil {
			return 0, err
		}
		runner := s.env.Runner
		if runner == nil {
			runner = toolexec.New(s.env.Vars)
		}
		report := doctor.Run(ctx, doctor.Deps{
			Resolver: toolexec.Resolver{Vars: s.env.Vars, Config: cfg.Tools},
			Runner:   runner,
			Env:      s.env.Vars,
		})
		if g.json {
			if err := s.printJSON(report); err != nil {
				return 0, err
			}
			return report.Exit(), nil
		}
		for _, c := range report.Checks {
			fmt.Fprintf(s.stdout, "[%s] %s", c.Status, c.Name)
			if c.Detail != "" {
				fmt.Fprintf(s.stdout, "：%s", c.Detail)
			}
			fmt.Fprintln(s.stdout)
			if c.Fix != "" {
				fmt.Fprintf(s.stdout, "      修正方式：%s\n", c.Fix)
			}
		}
		return report.Exit(), nil
	}}, nil
}
