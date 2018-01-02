package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vim-volt/volt/config"
	"github.com/vim-volt/volt/fileutil"
	"github.com/vim-volt/volt/gitutil"
	"github.com/vim-volt/volt/lockjson"
	"github.com/vim-volt/volt/logger"
	"github.com/vim-volt/volt/pathutil"
	"github.com/vim-volt/volt/plugconf"
	"github.com/vim-volt/volt/transaction"

	multierror "github.com/hashicorp/go-multierror"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/protocol/packp/sideband"
)

func init() {
	cmdMap["get"] = &getCmd{}
}

type getCmd struct {
	helped   bool
	lockJSON bool
	upgrade  bool
	verbose  bool
}

func (cmd *getCmd) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Println(`
Usage
  volt get [-help] [-l] [-u] [-v] [{repository} ...]

Quick example
  $ volt get tyru/caw.vim     # will install tyru/caw.vim plugin
  $ volt get -u tyru/caw.vim  # will upgrade tyru/caw.vim plugin
  $ volt get -l -u            # will upgrade all installed plugins
  $ volt get -v tyru/caw.vim  # will output more verbosely

  $ mkdir -p ~/volt/repos/localhost/local/hello/plugin
  $ echo 'command! Hello echom "hello"' >~/volt/repos/localhost/local/hello/plugin/hello.vim
  $ volt get localhost/local/hello     # will add the local repository as a plugin
  $ vim -c Hello                       # will output "hello"

Description
  Install or upgrade given {repository} list, or add local {repository} list as plugins.

  And fetch skeleton plugconf from:
    https://github.com/vim-volt/plugconf-templates
  and install it to:
    $VOLTPATH/plugconf/{repository}.vim

  If -v option was specified, output more verbosely.

Repository List
  {repository} list (=target to perform installing, upgrading, and so on) is determined as followings:
  * If -l option is specified, all installed vim plugins (regardless current profile) are used
  * If one or more {repository} arguments are specified, the arguments are used

Action
  The action (install, upgrade, or add only) is determined as follows:
    1. If -u option is specified (upgrade):
      * Upgrade git repositories in {repository} list (static repositories are ignored).
      * Add {repository} list to lock.json (if not found)
    2. Or (install):
      * Fetch {repository} list from remotes
      * Add {repository} list to lock.json (if not found)

Static repository
    Volt can manage a local directory as a repository. It's called "static repository".
    When you have unpublished plugins, or you want to manage ~/.vim/* files as one repository
    (this is useful when you use profile feature, see "volt help profile" for more details),
    static repository is useful.
    All you need is to create a directory in "$VOLTPATH/repos/<repos>".

    When -u was not specified (install) and given repositories exist, volt does not make a request to clone the repositories.
    Therefore, "volt get" tries to fetch repositories but skip it because the directory exists.
    then it adds repositories to lock.json if not found.

      $ mkdir -p ~/volt/repos/localhost/local/hello/plugin
      $ echo 'command! Hello echom "hello"' >~/volt/repos/localhost/local/hello/plugin/hello.vim
      $ volt get localhost/local/hello     # will add the local repository as a plugin
      $ vim -c Hello                       # will output "hello"

Repository path
  {repository}'s format is one of the followings:

  1. {user}/{name}
       This is same as "github.com/{user}/{name}"
  2. {site}/{user}/{name}
  3. https://{site}/{user}/{name}
  4. http://{site}/{user}/{name}

Options`)
		fs.PrintDefaults()
		fmt.Println()
		cmd.helped = true
	}
	fs.BoolVar(&cmd.lockJSON, "l", false, "use all installed repositories as targets")
	fs.BoolVar(&cmd.upgrade, "u", false, "upgrade repositories")
	fs.BoolVar(&cmd.verbose, "v", false, "output more verbosely")
	return fs
}

func (cmd *getCmd) Run(args []string) int {
	// Parse args
	args, err := cmd.parseArgs(args)
	if err == ErrShowedHelp {
		return 0
	}
	if err != nil {
		logger.Error("Failed to parse args: " + err.Error())
		return 10
	}

	// Read lock.json
	lockJSON, err := lockjson.Read()
	if err != nil {
		logger.Error("Could not read lock.json: " + err.Error())
		return 11
	}

	reposPathList, err := cmd.getReposPathList(args, lockJSON)
	if err != nil {
		logger.Error("Could not get repos list: " + err.Error())
		return 12
	}
	if len(reposPathList) == 0 {
		logger.Error("No repositories are specified")
		return 13
	}

	err = cmd.doGet(reposPathList, lockJSON)
	if err != nil {
		logger.Error(err.Error())
		return 20
	}

	return 0
}

func (cmd *getCmd) parseArgs(args []string) ([]string, error) {
	fs := cmd.FlagSet()
	fs.Parse(args)
	if cmd.helped {
		return nil, ErrShowedHelp
	}

	if !cmd.lockJSON && len(fs.Args()) == 0 {
		fs.Usage()
		return nil, errors.New("repository was not given")
	}

	return fs.Args(), nil
}

func (cmd *getCmd) getReposPathList(args []string, lockJSON *lockjson.LockJSON) ([]string, error) {
	reposPathList := make([]string, 0, 32)
	if cmd.lockJSON {
		for _, repos := range lockJSON.Repos {
			reposPathList = append(reposPathList, repos.Path)
		}
	} else {
		for _, arg := range args {
			reposPath, err := pathutil.NormalizeRepos(arg)
			if err != nil {
				return nil, err
			}
			reposPathList = append(reposPathList, reposPath)
		}
	}
	return reposPathList, nil
}

func (cmd *getCmd) doGet(reposPathList []string, lockJSON *lockjson.LockJSON) error {
	// Find matching profile
	profile, err := lockJSON.Profiles.FindByName(lockJSON.CurrentProfileName)
	if err != nil {
		// this must not be occurred because lockjson.Read()
		// validates if the matching profile exists
		return err
	}

	// Begin transaction
	err = transaction.Create()
	if err != nil {
		return err
	}
	defer transaction.Remove()
	lockJSON.TrxID++

	// Read config.toml
	cfg, err := config.Read()
	if err != nil {
		return errors.New("could not read config.toml: " + err.Error())
	}

	done := make(chan getParallelResult, len(reposPathList))
	getCount := 0
	// Invoke installing / upgrading tasks
	for _, reposPath := range reposPathList {
		repos, err := lockJSON.Repos.FindByPath(reposPath)
		if err != nil {
			repos = nil
		}
		if repos == nil || repos.Type == lockjson.ReposGitType {
			go cmd.getParallel(reposPath, repos, *cfg.Get.CreateSkeletonPlugconf, done)
			getCount++
		}
	}

	// Wait results
	failed := false
	statusList := make([]string, 0, getCount)
	var updatedLockJSON bool
	for i := 0; i < getCount; i++ {
		r := <-done
		status := cmd.formatStatus(&r)
		// Update repos[]/trx_id, repos[]/version
		if strings.HasPrefix(status, statusPrefixFailed) {
			failed = true
		} else {
			added := cmd.updateReposVersion(lockJSON, r.reposPath, r.reposType, r.hash, profile)
			if added && strings.Contains(status, "already exists") {
				status = fmt.Sprintf(fmtAddedRepos, statusPrefixInstalled, r.reposPath)
			}
			updatedLockJSON = true
		}
		statusList = append(statusList, status)
	}

	// Sort by status
	sort.Strings(statusList)

	if updatedLockJSON {
		// Write to lock.json
		err = lockJSON.Write()
		if err != nil {
			return errors.New("could not write to lock.json: " + err.Error())
		}
	}

	// Build ~/.vim/pack/volt dir
	err = (&buildCmd{}).doBuild(false)
	if err != nil {
		return errors.New("could not build " + pathutil.VimVoltDir() + ": " + err.Error())
	}

	// Show results
	for i := range statusList {
		fmt.Println(statusList[i])
	}
	if failed {
		return errors.New("failed to install some plugins")
	}
	return nil
}

func (*getCmd) formatStatus(r *getParallelResult) string {
	if r.err == nil {
		return r.status
	}
	var errs []error
	if merr, ok := r.err.(*multierror.Error); ok {
		errs = merr.Errors
	} else {
		errs = []error{r.err}
	}
	buf := make([]byte, 0, 4*1024)
	buf = append(buf, r.status...)
	for _, err := range errs {
		buf = append(buf, "\n  * "...)
		buf = append(buf, err.Error()...)
	}
	return string(buf)
}

type getParallelResult struct {
	reposPath string
	status    string
	hash      string
	reposType lockjson.ReposType
	err       error
}

const (
	statusPrefixFailed    = "!"
	statusPrefixNoChange  = "#"
	statusPrefixInstalled = "+"
	statusPrefixUpgraded  = "*"
)

const (
	fmtInstallFailed = "%s %s > install failed > %s"
	fmtUpgradeFailed = "%s %s > upgrade failed > %s"
	fmtNoChange      = "%s %s > no change"
	fmtAlreadyExists = "%s %s > already exists"
	fmtAddedRepos    = "%s %s > added repository to current profile"
	fmtInstalled     = "%s %s > installed"
	fmtRevUpdate     = "%s %s > updated lock.json revision (%s..%s)"
	fmtUpgraded      = "%s %s > upgraded (%s..%s)"
)

// This function is executed in goroutine of each plugin.
// 1. install plugin if it does not exist
// 2. install plugconf if it does not exist and createPlugconf=true
func (cmd *getCmd) getParallel(reposPath string, repos *lockjson.Repos, createPlugconf bool, done chan<- getParallelResult) {
	pluginDone := make(chan getParallelResult)
	go cmd.installPlugin(reposPath, repos, pluginDone)
	pluginResult := <-pluginDone
	if pluginResult.err != nil || !createPlugconf {
		done <- pluginResult
		return
	}
	plugconfDone := make(chan getParallelResult)
	go cmd.installPlugconf(reposPath, &pluginResult, plugconfDone)
	done <- (<-plugconfDone)
}

func (cmd *getCmd) installPlugin(reposPath string, repos *lockjson.Repos, done chan<- getParallelResult) {
	// true:upgrade, false:install
	fullReposPath := pathutil.FullReposPathOf(reposPath)
	doUpgrade := cmd.upgrade && pathutil.Exists(fullReposPath)

	var fromHash string
	var err error
	if doUpgrade {
		// Get HEAD hash string
		fromHash, err = gitutil.GetHEAD(reposPath)
		if err != nil {
			result := errors.New("failed to get HEAD commit hash: " + err.Error())
			if cmd.verbose {
				logger.Info("Rollbacking " + fullReposPath + " ...")
			} else {
				logger.Debug("Rollbacking " + fullReposPath + " ...")
			}
			err = cmd.rollbackRepos(fullReposPath)
			if err != nil {
				result = multierror.Append(result, err)
			}
			done <- getParallelResult{
				reposPath: reposPath,
				status:    fmt.Sprintf(fmtInstallFailed, statusPrefixFailed, reposPath, result.Error()),
				err:       result,
			}
			return
		}
	}

	var status string
	var upgraded bool

	if doUpgrade {
		// when cmd.upgrade is true, repos must not be nil.
		if repos == nil {
			msg := "-u was specified but repos == nil"
			done <- getParallelResult{
				reposPath: reposPath,
				status:    fmt.Sprintf(fmtUpgradeFailed, statusPrefixFailed, reposPath, msg),
				err:       errors.New("failed to upgrade plugin: " + msg),
			}
			return
		}
		// Upgrade plugin
		if cmd.verbose {
			logger.Info("Upgrading " + reposPath + " ...")
		} else {
			logger.Debug("Upgrading " + reposPath + " ...")
		}
		err := cmd.upgradePlugin(reposPath)
		if err != git.NoErrAlreadyUpToDate && err != nil {
			result := errors.New("failed to upgrade plugin: " + err.Error())
			if cmd.verbose {
				logger.Info("Rollbacking " + fullReposPath + " ...")
			} else {
				logger.Debug("Rollbacking " + fullReposPath + " ...")
			}
			err = cmd.rollbackRepos(fullReposPath)
			if err != nil {
				result = multierror.Append(result, err)
			}
			done <- getParallelResult{
				reposPath: reposPath,
				status:    fmt.Sprintf(fmtUpgradeFailed, statusPrefixFailed, reposPath, err.Error()),
				err:       result,
			}
			return
		}
		if err == git.NoErrAlreadyUpToDate {
			status = fmt.Sprintf(fmtNoChange, statusPrefixNoChange, reposPath)
		} else {
			upgraded = true
		}
	} else if !pathutil.Exists(fullReposPath) {
		// Install plugin
		if cmd.verbose {
			logger.Info("Installing " + reposPath + " ...")
		} else {
			logger.Debug("Installing " + reposPath + " ...")
		}
		err := cmd.fetchPlugin(reposPath)
		// if err == errRepoExists, silently skip
		if err != nil && err != errRepoExists {
			result := errors.New("failed to install plugin: " + err.Error())
			if cmd.verbose {
				logger.Info("Rollbacking " + fullReposPath + " ...")
			} else {
				logger.Debug("Rollbacking " + fullReposPath + " ...")
			}
			err = cmd.rollbackRepos(fullReposPath)
			if err != nil {
				result = multierror.Append(result, err)
			}
			done <- getParallelResult{
				reposPath: reposPath,
				status:    fmt.Sprintf(fmtInstallFailed, statusPrefixFailed, reposPath, result.Error()),
				err:       result,
			}
			return
		}
		if err == errRepoExists {
			status = fmt.Sprintf(fmtAlreadyExists, statusPrefixNoChange, reposPath)
		} else {
			status = fmt.Sprintf(fmtInstalled, statusPrefixInstalled, reposPath)
		}
	}

	var toHash string
	reposType, err := cmd.detectReposType(fullReposPath)
	if err == nil && reposType == lockjson.ReposGitType {
		// Get HEAD hash string
		toHash, err = gitutil.GetHEAD(reposPath)
		if err != nil {
			result := errors.New("failed to get HEAD commit hash: " + err.Error())
			if cmd.verbose {
				logger.Info("Rollbacking " + fullReposPath + " ...")
			} else {
				logger.Debug("Rollbacking " + fullReposPath + " ...")
			}
			err = cmd.rollbackRepos(fullReposPath)
			if err != nil {
				result = multierror.Append(result, err)
			}
			done <- getParallelResult{
				reposPath: reposPath,
				status:    fmt.Sprintf(fmtInstallFailed, statusPrefixFailed, reposPath, result.Error()),
				err:       result,
			}
			return
		}
	}

	// Show old and new revisions: "upgraded ({from}..{to})".
	if upgraded {
		status = fmt.Sprintf(fmtUpgraded, statusPrefixUpgraded, reposPath, fromHash, toHash)
	}

	if repos != nil && repos.Version != toHash {
		status = fmt.Sprintf(fmtRevUpdate, statusPrefixUpgraded, reposPath, repos.Version, toHash)
	} else {
		status = fmt.Sprintf(fmtNoChange, statusPrefixNoChange, reposPath)
	}

	done <- getParallelResult{
		reposPath: reposPath,
		status:    status,
		reposType: reposType,
		hash:      toHash,
	}
}

func (cmd *getCmd) installPlugconf(reposPath string, pluginResult *getParallelResult, done chan<- getParallelResult) {
	// Install plugconf
	if cmd.verbose {
		logger.Info("Installing plugconf " + reposPath + " ...")
	} else {
		logger.Debug("Installing plugconf " + reposPath + " ...")
	}
	err := cmd.fetchPlugconf(reposPath)
	if err != nil {
		result := errors.New("failed to install plugconf: " + err.Error())
		fullReposPath := pathutil.FullReposPathOf(reposPath)
		if cmd.verbose {
			logger.Info("Rollbacking " + fullReposPath + " ...")
		} else {
			logger.Debug("Rollbacking " + fullReposPath + " ...")
		}
		err = cmd.rollbackRepos(fullReposPath)
		if err != nil {
			result = multierror.Append(result, err)
		}
		done <- getParallelResult{
			reposPath: reposPath,
			status:    fmt.Sprintf(fmtInstallFailed, statusPrefixFailed, reposPath, result.Error()),
			err:       result,
		}
		return
	}
	done <- *pluginResult
}

func (*getCmd) detectReposType(fullpath string) (lockjson.ReposType, error) {
	if pathutil.Exists(filepath.Join(fullpath, ".git")) {
		if _, err := git.PlainOpen(fullpath); err != nil {
			return "", err
		}
		return lockjson.ReposGitType, nil
	}
	return lockjson.ReposStaticType, nil
}

func (*getCmd) rollbackRepos(fullReposPath string) error {
	if pathutil.Exists(fullReposPath) {
		err := os.RemoveAll(fullReposPath)
		if err != nil {
			return fmt.Errorf("rollback failed: cannot remove '%s'", fullReposPath)
		}
		// Remove parent directories
		fileutil.RemoveDirs(filepath.Dir(fullReposPath))
	}
	return nil
}

func (cmd *getCmd) upgradePlugin(reposPath string) error {
	fullpath := pathutil.FullReposPathOf(reposPath)

	var progress sideband.Progress = nil
	// if cmd.verbose {
	// 	progress = os.Stdout
	// }

	repos, err := git.PlainOpen(fullpath)
	if err != nil {
		return err
	}

	cfg, err := repos.Config()
	if err != nil {
		return err
	}

	if cfg.Core.IsBare {
		return repos.Fetch(&git.FetchOptions{
			RemoteName: "origin",
			Progress:   progress,
		})
	} else {
		wt, err := repos.Worktree()
		if err != nil {
			return err
		}
		return wt.Pull(&git.PullOptions{
			RemoteName: "origin",
			Progress:   progress,
		})
	}
}

var errRepoExists = errors.New("repository exists")

func (cmd *getCmd) fetchPlugin(reposPath string) error {
	fullpath := pathutil.FullReposPathOf(reposPath)
	if pathutil.Exists(fullpath) {
		return errRepoExists
	}

	var progress sideband.Progress = nil
	// if cmd.verbose {
	// 	progress = os.Stdout
	// }

	err := os.MkdirAll(filepath.Dir(fullpath), 0755)
	if err != nil {
		return err
	}

	// Clone repository to $VOLTPATH/repos/{site}/{user}/{name}
	isBare := false
	r, err := git.PlainClone(fullpath, isBare, &git.CloneOptions{
		URL:      pathutil.CloneURLOf(reposPath),
		Progress: progress,
	})
	if err != nil {
		return err
	}

	return gitutil.SetUpstreamBranch(r)
}

func (cmd *getCmd) fetchPlugconf(reposPath string) error {
	filename := pathutil.PlugconfOf(reposPath)
	if pathutil.Exists(filename) {
		logger.Debugf("plugconf '%s' exists... skip", filename)
		return nil
	}

	// If non-nil error returned from FetchPlugconf(),
	// create skeleton plugconf file
	tmpl, err := plugconf.FetchPlugconf(reposPath)
	if err != nil {
		logger.Debug(err.Error())
	}
	content, err := plugconf.GenPlugconfByTemplate(tmpl, filename)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(filename), 0755)
	err = ioutil.WriteFile(filename, content, 0644)
	if err != nil {
		return err
	}
	return nil
}

// * Add repos to 'repos' if not found
// * Add repos to 'profiles[]/repos_path' if not found
func (*getCmd) updateReposVersion(lockJSON *lockjson.LockJSON, reposPath string, reposType lockjson.ReposType, version string, profile *lockjson.Profile) bool {
	repos, err := lockJSON.Repos.FindByPath(reposPath)
	if err != nil {
		repos = nil
	}

	added := false

	if repos == nil {
		// repos is not found in lock.json
		// -> previous operation is install
		repos = &lockjson.Repos{
			Type:    reposType,
			TrxID:   lockJSON.TrxID,
			Path:    reposPath,
			Version: version,
		}
		// Add repos to 'repos'
		lockJSON.Repos = append(lockJSON.Repos, *repos)
		added = true
	} else {
		// repos is found in lock.json
		// -> previous operation is upgrade
		repos.TrxID = lockJSON.TrxID
		repos.Version = version
	}

	if !profile.ReposPath.Contains(reposPath) {
		// Add repos to 'profiles[]/repos_path'
		profile.ReposPath = append(profile.ReposPath, reposPath)
		added = true
	}
	return added
}
