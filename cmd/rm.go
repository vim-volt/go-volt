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

type rmCmd struct{}

func Rm(args []string) int {
	cmd := rmCmd{}

	reposPath, err := cmd.parseArgs(args)
	if err != nil {
		logger.Error(err.Error())
		return 10
	}

	err = cmd.removeRepos(reposPath)
	if err != nil {
		logger.Error("Failed to remove repository: " + err.Error())
		return 11
	}

	// Rebuild start dir
	err = (&rebuildCmd{}).doRebuild(false)
	if err != nil {
		logger.Error("Could not rebuild " + pathutil.VimVoltStartDir() + ": " + err.Error())
		return 12
	}

	return 0
}

func (rmCmd) parseArgs(args []string) (string, error) {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Println(`
Usage
  volt rm [-help] {repository}

Description
  Uninstall vim plugin and system plugconf files

Options`)
		fs.PrintDefaults()
		fmt.Println()
	}
	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fs.Usage()
		return "", errors.New("repository was not given")
	}

	reposPath, err := pathutil.NormalizeRepos(fs.Args()[0])
	if err != nil {
		return "", err
	}
	return reposPath, nil
}

func (cmd rmCmd) removeRepos(reposPath string) error {
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

	// Remove system plugconf
	logger.Info("Removing plugconf files ...")
	plugConf := pathutil.SystemPlugConfOf(reposPath + ".vim")
	if pathutil.Exists(plugConf) {
		err = os.Remove(plugConf)
		if err != nil {
			return err
		}
	}

	// Remove parent directories of system plugconf
	dir, _ := filepath.Split(pathutil.SystemPlugConfOf(reposPath))
	err = cmd.removeDirs(dir)

	// Remove existing repository
	fullpath := pathutil.FullReposPathOf(reposPath)
	logger.Info("Removing " + fullpath + " ...")
	if pathutil.Exists(fullpath) {
		err = os.RemoveAll(fullpath)
		if err != nil {
			return err
		}
		dir, _ := filepath.Split(fullpath)
		cmd.removeDirs(dir)
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

	// Write to lock.json
	err = lockJSON.Write()
	if err != nil {
		return err
	}

	return nil
}

func (cmd rmCmd) removeDirs(dir string) error {
	// Remove trailing slashes
	dir = strings.TrimRight(dir, "/")

	if err := os.Remove(dir); err != nil {
		return err
	} else {
		parent, _ := filepath.Split(dir)
		return cmd.removeDirs(parent)
	}
}
