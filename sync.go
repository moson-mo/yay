package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Jguer/yay/v11/pkg/completion"
	"github.com/Jguer/yay/v11/pkg/db"
	"github.com/Jguer/yay/v11/pkg/dep"
	"github.com/Jguer/yay/v11/pkg/multierror"
	"github.com/Jguer/yay/v11/pkg/settings"
	"github.com/Jguer/yay/v11/pkg/settings/parser"
	"github.com/Jguer/yay/v11/pkg/text"

	"github.com/leonelquinteros/gotext"
)

func syncInstall(ctx context.Context,
	config *settings.Configuration,
	cmdArgs *parser.Arguments,
	dbExecutor db.Executor,
) error {
	aurCache := config.Runtime.AURCache
	refreshArg := cmdArgs.ExistsArg("y", "refresh")
	noDeps := cmdArgs.ExistsArg("d", "nodeps")
	noCheck := strings.Contains(config.MFlags, "--nocheck")
	if noDeps {
		config.Runtime.CmdBuilder.AddMakepkgFlag("-d")
	}

	if refreshArg && config.Runtime.Mode.AtLeastRepo() {
		if errR := earlyRefresh(ctx, cmdArgs); errR != nil {
			return fmt.Errorf("%s - %w", gotext.Get("error refreshing databases"), errR)
		}

		// we may have done -Sy, our handle now has an old
		// database.
		if errRefresh := dbExecutor.RefreshHandle(); errRefresh != nil {
			return errRefresh
		}
	}

	grapher := dep.NewGrapher(dbExecutor, aurCache, false, settings.NoConfirm, os.Stdout, noDeps, noCheck)

	graph, err := grapher.GraphFromTargets(ctx, nil, cmdArgs.Targets)
	if err != nil {
		return err
	}

	if cmdArgs.ExistsArg("u", "sysupgrade") {
		var errSysUp error

		graph, _, errSysUp = sysupgradeTargetsV2(ctx, aurCache, dbExecutor, graph, cmdArgs.ExistsDouble("u", "sysupgrade"))
		if errSysUp != nil {
			return errSysUp
		}
	}

	opService := NewOperationService(ctx, config, dbExecutor)
	multiErr := &multierror.MultiError{}
	targets := graph.TopoSortedLayerMap(func(s string, ii *dep.InstallInfo) error {
		if ii.Source == dep.Missing {
			multiErr.Add(errors.New(gotext.Get("could not find %s%s", s, ii.Version)))
		}
		return nil
	})

	if err := multiErr.Return(); err != nil {
		return err
	}
	return opService.Run(ctx, cmdArgs, targets)
}

type OperationService struct {
	ctx        context.Context
	cfg        *settings.Configuration
	dbExecutor db.Executor
}

func NewOperationService(ctx context.Context, cfg *settings.Configuration, dbExecutor db.Executor) *OperationService {
	return &OperationService{
		ctx:        ctx,
		cfg:        cfg,
		dbExecutor: dbExecutor,
	}
}

func (o *OperationService) Run(ctx context.Context,
	cmdArgs *parser.Arguments,
	targets []map[string]*dep.InstallInfo,
) error {
	if len(targets) == 0 {
		fmt.Fprintln(os.Stdout, "", gotext.Get("there is nothing to do"))
		return nil
	}
	preparer := NewPreparer(o.dbExecutor, o.cfg.Runtime.CmdBuilder, o.cfg)
	installer := NewInstaller(o.dbExecutor, o.cfg.Runtime.CmdBuilder, o.cfg.Runtime.VCSStore, o.cfg.Runtime.Mode)

	pkgBuildDirs, err := preparer.Run(ctx, os.Stdout, targets)
	if err != nil {
		return err
	}

	cleanFunc := preparer.ShouldCleanMakeDeps()
	if cleanFunc != nil {
		installer.AddPostInstallHook(cleanFunc)
	}

	if cleanAURDirsFunc := preparer.ShouldCleanAURDirs(pkgBuildDirs); cleanAURDirsFunc != nil {
		installer.AddPostInstallHook(cleanAURDirsFunc)
	}

	srcinfoOp := srcinfoOperator{
		dbExecutor: o.dbExecutor,
		cfg:        o.cfg,
		cmdBuilder: installer.exeCmd,
	}
	srcinfos, err := srcinfoOp.Run(ctx, pkgBuildDirs)
	if err != nil {
		return err
	}

	go func() {
		_ = completion.Update(ctx, o.cfg.Runtime.HTTPClient, o.dbExecutor,
			o.cfg.AURURL, o.cfg.Runtime.CompletionPath, o.cfg.CompletionInterval, false)
	}()

	err = installer.Install(ctx, cmdArgs, targets, pkgBuildDirs, srcinfos)
	if err != nil {
		if errHook := installer.RunPostInstallHooks(ctx); errHook != nil {
			text.Errorln(errHook)
		}

		return err
	}

	var multiErr multierror.MultiError

	if err := installer.CompileFailedAndIgnored(); err != nil {
		multiErr.Add(err)
	}

	if err := installer.RunPostInstallHooks(ctx); err != nil {
		multiErr.Add(err)
	}

	return multiErr.Return()
}