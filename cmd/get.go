package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/vim-volt/go-volt/lockjson"
	"github.com/vim-volt/go-volt/pathutil"
	"github.com/vim-volt/go-volt/transaction"

	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/protocol/packp/sideband"
)

type getCmd struct{}

type getFlags struct {
	lockJSON bool
	upgrade  bool
	verbose  bool
}

var ErrRepoExists = errors.New("repository exists")

func Get(args []string) int {
	cmd := getCmd{}

	// Parse args
	args, flags, err := cmd.parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 10
	}

	// Read lock.json
	lockJSON, err := lockjson.Read()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] Could not read lock.json: "+err.Error())
		return 11
	}

	reposPathList, err := cmd.getReposPathList(flags, args, lockJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] Could not get repos list: "+err.Error())
		return 12
	}

	// Check if any repositories are dirty
	for _, reposPath := range reposPathList {
		fullpath := pathutil.FullReposPathOf(reposPath)
		if cmd.pathExists(fullpath) && cmd.isDirtyWorktree(fullpath) {
			fmt.Fprintln(os.Stderr, "[ERROR] Repository has dirty worktree: "+fullpath)
			return 13
		}
	}

	// Begin transaction
	err = transaction.Create()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] Failed to begin transaction: "+err.Error())
		return 14
	}
	defer transaction.Remove()

	var updatedLockJSON bool
	var upgradedList []string
	for _, reposPath := range reposPathList {
		upgrade := flags.upgrade && cmd.pathExists(pathutil.FullReposPathOf(reposPath))

		// Install / Upgrade plugin
		err = cmd.doGet(reposPath, flags)
		if err == nil {
			// Get HEAD hash string
			hash, err := cmd.getHEADHashString(reposPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[ERROR] Failed to get HEAD commit hash: "+err.Error())
				continue
			}
			// Update repos[]/trx_id, repos[]/version
			cmd.updateReposVersion(lockJSON, reposPath, hash)
			updatedLockJSON = true
			// Collect upgraded repos path
			if upgrade {
				upgradedList = append(upgradedList, reposPath)
			}
		} else {
			fmt.Fprintln(os.Stderr, "[ERROR] Failed to install / upgrade plugins: "+err.Error())
		}
	}

	if updatedLockJSON {
		err = lockjson.Write(lockJSON)
		if err != nil {
			fmt.Fprintln(os.Stderr, "[ERROR] Could not write to lock.json: "+err.Error())
			return 15
		}
	}

	// Show upgraded plugins
	if len(upgradedList) > 0 {
		fmt.Fprintln(os.Stderr, "[WARN] Reloading upgraded plugin is not supported.")
		fmt.Fprintln(os.Stderr, "[WARN] Please restart your Vim to reload the following plugins:")
		for _, reposPath := range upgradedList {
			fmt.Fprintln(os.Stderr, "[WARN]   "+reposPath)
		}
	}

	return 0
}

func (getCmd) parseArgs(args []string) ([]string, *getFlags, error) {
	var flags getFlags
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `
Usage
  volt get [-help] [-l] [-u] [-v] [{repository} ...]

Description
  Install / Upgrade vim plugin.

Options`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.BoolVar(&flags.lockJSON, "l", false, "from lock.json")
	fs.BoolVar(&flags.upgrade, "u", false, "upgrade installed vim plugin")
	fs.BoolVar(&flags.verbose, "v", false, "show git-clone output")
	fs.Parse(args)

	if !flags.lockJSON && len(fs.Args()) == 0 {
		fs.Usage()
		return nil, nil, errors.New("repository was not given")
	}
	return fs.Args(), &flags, nil
}

func (getCmd) getReposPathList(flags *getFlags, args []string, lockJSON *lockjson.LockJSON) ([]string, error) {
	reposPathList := make([]string, 0, 32)
	if flags.lockJSON {
		for _, repos := range lockJSON.Repos {
			reposPathList = append(reposPathList, repos.Path)
		}
	} else {
		for _, arg := range args {
			reposPath, err := pathutil.NormalizeRepository(arg)
			if err != nil {
				return nil, err
			}
			reposPathList = append(reposPathList, reposPath)
		}
	}
	return reposPathList, nil
}

func (getCmd) pathExists(fullpath string) bool {
	_, err := os.Stat(fullpath)
	return !os.IsNotExist(err)
}

func (getCmd) isDirtyWorktree(fullpath string) bool {
	repos, err := git.PlainOpen(fullpath)
	if err != nil {
		return true
	}
	wt, err := repos.Worktree()
	if err != nil {
		return true
	}
	st, err := wt.Status()
	if err != nil {
		return true
	}
	return !st.IsClean()
}

func (cmd getCmd) doGet(reposPath string, flags *getFlags) error {
	fullpath := pathutil.FullReposPathOf(reposPath)
	if !flags.upgrade && cmd.pathExists(fullpath) {
		return ErrRepoExists
	}

	if flags.upgrade {
		fmt.Println("[INFO] Upgrading " + reposPath + " ...")
	} else {
		fmt.Println("[INFO] Installing " + reposPath + " ...")
	}

	// Get existing temporary directory path
	err := os.MkdirAll(pathutil.TempPath(), 0755)
	if err != nil {
		return err
	}
	tempPath, err := ioutil.TempDir(pathutil.TempPath(), "volt-")
	if err != nil {
		return err
	}
	err = os.MkdirAll(tempPath, 0755)
	if err != nil {
		return err
	}

	var progress sideband.Progress = nil
	if flags.verbose {
		progress = os.Stdout
	}

	// git clone to temporary directory
	tempGitRepos, err := git.PlainClone(tempPath, false, &git.CloneOptions{
		URL:      pathutil.CloneURLOf(reposPath),
		Progress: progress,
	})
	if err != nil {
		return err
	}

	// If !flags.upgrade or HEAD was changed (= the plugin is outdated) ...
	if !flags.upgrade || cmd.headWasChanged(reposPath, tempGitRepos) {
		// Remove existing repository
		if cmd.pathExists(fullpath) {
			err = os.RemoveAll(fullpath)
			if err != nil {
				return err
			}
		}

		// Move repository to $VOLTPATH/repos/{site}/{user}/{name}
		err = os.MkdirAll(filepath.Dir(fullpath), 0755)
		if err != nil {
			return err
		}
		err = os.Rename(tempPath, fullpath)
		if err != nil {
			return err
		}

		// TODO: Fetch plugconf
		fmt.Println("[INFO] Installing plugconf " + reposPath + " ...")
	}

	return nil
}

func (cmd getCmd) headWasChanged(reposPath string, tempGitRepos *git.Repository) bool {
	tempHead, err := tempGitRepos.Head()
	if err != nil {
		return false
	}
	hash, err := cmd.getHEADHashString(reposPath)
	if err != nil {
		return false
	}
	return tempHead.Hash().String() != hash
}

func (getCmd) updateReposVersion(lockJSON *lockjson.LockJSON, reposPath string, version string) {
	var r *lockjson.Repos
	for i := range lockJSON.Repos {
		if lockJSON.Repos[i].Path == reposPath {
			r = &lockJSON.Repos[i]
			break
		}
	}

	if r == nil {
		// vim plugin is not found in lock.json
		// -> previous operation is install
		lockJSON.Repos = append(lockJSON.Repos, lockjson.Repos{
			TrxID:   lockJSON.TrxID,
			Path:    reposPath,
			Version: version,
			Active:  true,
		})
	} else {
		// vim plugin is found in lock.json
		// -> previous operation is upgrade
		r.TrxID = lockJSON.TrxID
		r.Version = version
	}
}

func (getCmd) getHEADHashString(reposPath string) (string, error) {
	repos, err := git.PlainOpen(pathutil.FullReposPathOf(reposPath))
	if err != nil {
		return "", err
	}
	head, err := repos.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}
