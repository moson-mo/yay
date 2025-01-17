package main

import (
	"context"
	"os"
	"sync"

	"github.com/Jguer/aur"
	"github.com/leonelquinteros/gotext"

	"github.com/Jguer/yay/v12/pkg/db"
	"github.com/Jguer/yay/v12/pkg/dep"
	"github.com/Jguer/yay/v12/pkg/settings"
	"github.com/Jguer/yay/v12/pkg/srcinfo"
	"github.com/Jguer/yay/v12/pkg/text"
)

func infoToInstallInfo(info []aur.Pkg) []map[string]*dep.InstallInfo {
	installInfo := make([]map[string]*dep.InstallInfo, 1)
	installInfo[0] = map[string]*dep.InstallInfo{}

	for i := range info {
		pkg := &info[i]
		installInfo[0][pkg.Name] = &dep.InstallInfo{
			AURBase: &pkg.PackageBase,
			Source:  dep.AUR,
		}
	}

	return installInfo
}

// createDevelDB forces yay to create a DB of the existing development packages.
func createDevelDB(ctx context.Context, cfg *settings.Configuration, dbExecutor db.Executor) error {
	remoteNames := dbExecutor.InstalledRemotePackageNames()

	cfg.Runtime.QueryBuilder.Execute(ctx, dbExecutor, remoteNames)
	info, err := cfg.Runtime.AURClient.Get(ctx, &aur.Query{
		Needles:  remoteNames,
		By:       aur.Name,
		Contains: false,
	})
	if err != nil {
		return err
	}

	preper := NewPreparerWithoutHooks(dbExecutor, cfg.Runtime.CmdBuilder, cfg, false)

	mapInfo := infoToInstallInfo(info)
	pkgBuildDirsByBase, err := preper.Run(ctx, os.Stdout, mapInfo)
	if err != nil {
		return err
	}

	srcinfos, err := srcinfo.ParseSrcinfoFilesByBase(pkgBuildDirsByBase, false)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for i := range srcinfos {
		for iP := range srcinfos[i].Packages {
			wg.Add(1)

			go func(i string, iP int) {
				cfg.Runtime.VCSStore.Update(ctx, srcinfos[i].Packages[iP].Pkgname, srcinfos[i].Source)
				wg.Done()
			}(i, iP)
		}
	}

	wg.Wait()
	text.OperationInfoln(gotext.Get("GenDB finished. No packages were installed"))

	return err
}
