package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/Jguer/aur"
	"github.com/Jguer/votar/pkg/vote"
	"github.com/leonelquinteros/gotext"
)

type ErrAURVote struct {
	inner   error
	pkgName string
}

func (e *ErrAURVote) Error() string {
	return gotext.Get("Unable to handle package vote for: %s. err: %s", e.pkgName, e.inner.Error())
}

func handlePackageVote(ctx context.Context,
	targets []string, aurClient aur.QueryClient,
	voteClient *vote.Client, upvote bool,
) error {
	infos, err := aurClient.Get(ctx, &aur.Query{
		Needles: targets,
		By:      aur.Name,
	})
	if err != nil {
		return err
	}

	if len(infos) == 0 {
		fmt.Println(gotext.Get(" there is nothing to do"))
		return nil
	}

	for i := range infos {
		var err error
		if upvote {
			err = voteClient.Vote(ctx, infos[i].PackageBase)
		} else {
			err = voteClient.Unvote(ctx, infos[i].PackageBase)
		}

		if err != nil {
			if errors.Is(err, vote.ErrNoCredentials) {
				return errors.New(
					gotext.Get("%s: please set AUR_USERNAME and AUR_PASSWORD environment variables for voting",
						err.Error()))
			}

			return &ErrAURVote{inner: err, pkgName: infos[i].Name}
		}
	}

	return nil
}
