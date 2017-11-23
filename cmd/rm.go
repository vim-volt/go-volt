package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vim-volt/volt/lockjson"
	"github.com/vim-volt/volt/logger"
	"github.com/vim-volt/volt/pathutil"
	"github.com/vim-volt/volt/transaction"
)

type rmFlagsType struct {
	helped bool
}

var rmFlags rmFlagsType

func init() {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Print(`
Usage
  volt rm [-help] {repository} [{repository2} ...]

Quick example
  $ volt rm tyru/caw.vim # Uninstall tyru/caw.vim plugin

Description
  Uninstall vim plugin of {repository} on every profile.

  {repository} is treated as same format as "volt get" (see "volt get -help").` + "\n\n")
		//fmt.Println("Options")
		//fs.PrintDefaults()
		fmt.Println()
		rmFlags.helped = true
	}

	cmdFlagSet["rm"] = fs
}

type rmCmd struct{}

func Rm(args []string) int {
	cmd := rmCmd{}

	reposPathList, err := cmd.parseArgs(args)
	if err == ErrShowedHelp {
		return 0
	}
	if err != nil {
		logger.Error(err.Error())
		return 10
	}

	err = cmd.doRemove(reposPathList)
	if err != nil {
		logger.Error("Failed to remove repository: " + err.Error())
		return 11
	}

	// Rebuild opt dir
	err = (&rebuildCmd{}).doRebuild(false)
	if err != nil {
		logger.Error("could not rebuild " + pathutil.VimVoltDir() + ": " + err.Error())
		return 12
	}

	return 0
}

func (*rmCmd) parseArgs(args []string) ([]string, error) {
	fs := cmdFlagSet["rm"]
	fs.Parse(args)
	if rmFlags.helped {
		return nil, ErrShowedHelp
	}

	if len(fs.Args()) == 0 {
		fs.Usage()
		return nil, errors.New("repository was not given")
	}

	var reposPathList []string
	for _, arg := range fs.Args() {
		reposPath, err := pathutil.NormalizeRepos(arg)
		if err != nil {
			return nil, err
		}
		reposPathList = append(reposPathList, reposPath)
	}
	return reposPathList, nil
}

func (cmd *rmCmd) doRemove(reposPathList []string) error {
	// Read lock.json
	lockJSON, err := lockjson.Read()
	if err != nil {
		return err
	}

	// Begin transaction
	err = transaction.Create()
	if err != nil {
		return err
	}
	defer transaction.Remove()
	lockJSON.TrxID++

	// Remove each repository
	for _, reposPath := range reposPathList {
		err = cmd.removeRepos(reposPath, lockJSON)
		if err != nil {
			return err
		}
	}

	// Write to lock.json
	return lockJSON.Write()
}

func (cmd *rmCmd) removeRepos(reposPath string, lockJSON *lockjson.LockJSON) error {
	// Remove system plugconf
	logger.Info("Removing plugconf files ...")
	plugconf := pathutil.SystemPlugconfOf(reposPath)
	if pathutil.Exists(plugconf) {
		err := os.Remove(plugconf)
		if err != nil {
			return err
		}
	}

	// Remove parent directories of system plugconf
	err := cmd.removeDirs(filepath.Dir(pathutil.SystemPlugconfOf(reposPath)))

	// Remove existing repository
	fullpath := pathutil.FullReposPathOf(reposPath)
	logger.Info("Removing " + fullpath + " ...")
	if pathutil.Exists(fullpath) {
		err = os.RemoveAll(fullpath)
		if err != nil {
			return err
		}
		cmd.removeDirs(filepath.Dir(fullpath))
	} else {
		return errors.New("no repository was installed: " + fullpath)
	}

	// Delete repos path from lockJSON.Repos[i]
	err = lockJSON.Repos.RemoveAllByPath(reposPath)
	if err != nil {
		return err
	}

	// Delete repos path from profiles[i]/repos_path[j]
	lockJSON.Profiles.RemoveAllReposPath(reposPath)

	return nil
}

func (cmd *rmCmd) removeDirs(dir string) error {
	// Remove trailing slashes
	dir = strings.TrimRight(dir, "/")

	if err := os.Remove(dir); err != nil {
		return err
	} else {
		return cmd.removeDirs(filepath.Dir(dir))
	}
}
