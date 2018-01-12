package builder

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/vim-volt/volt/cmd/buildinfo"
	"github.com/vim-volt/volt/fileutil"
	"github.com/vim-volt/volt/gitutil"
	"github.com/vim-volt/volt/lockjson"
	"github.com/vim-volt/volt/logger"
	"github.com/vim-volt/volt/pathutil"
	"github.com/vim-volt/volt/plugconf"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

type copyBuilder struct {
	BaseBuilder
}

func (builder *copyBuilder) Build(buildInfo *buildinfo.BuildInfo, buildReposMap map[string]*buildinfo.Repos) error {
	// Exit if vim executable was not found in PATH
	vimExePath, err := pathutil.VimExecutable()
	if err != nil {
		return err
	}

	// Read lock.json
	lockJSON, err := lockjson.Read()
	if err != nil {
		return errors.New("could not read lock.json: " + err.Error())
	}

	// Get current profile's repos list
	reposList, err := builder.getCurrentReposList(lockJSON)
	if err != nil {
		return err
	}

	logger.Info("Installing vimrc and gvimrc ...")

	vimDir := pathutil.VimDir()
	vimrcPath := filepath.Join(vimDir, pathutil.Vimrc)
	gvimrcPath := filepath.Join(vimDir, pathutil.Gvimrc)
	err = builder.installVimrcAndGvimrc(
		lockJSON.CurrentProfileName, vimrcPath, gvimrcPath,
	)
	if err != nil {
		return err
	}

	// Mkdir opt dir
	optDir := pathutil.VimVoltOptDir()
	os.MkdirAll(optDir, 0755)
	if !pathutil.Exists(optDir) {
		return errors.New("could not create " + optDir)
	}

	reposDirList, err := ioutil.ReadDir(pathutil.VimVoltOptDir())
	if err != nil {
		return err
	}

	// Copy volt repos files to optDir
	copyDone, copyCount := builder.copyReposList(buildReposMap, reposList, optDir, vimExePath)

	// Remove vim repos not found in lock.json current repos list
	removeDone, removeCount := builder.removeReposList(reposList, reposDirList)

	// Wait copy
	var copyModified bool
	copyErr := builder.waitCopyRepos(copyDone, copyCount, func(result *actionReposResult) error {
		logger.Info("Installing " + string(result.repos.Type) + " repository " + result.repos.Path + " ... Done.")
		// Construct buildInfo from the result
		builder.constructBuildInfo(buildInfo, result)
		copyModified = true
		return nil
	})

	// Wait remove
	var removeModified bool
	removeErr := builder.waitRemoveRepos(removeDone, removeCount, func(result *actionReposResult) {
		// Remove the repository from buildInfo
		buildInfo.Repos.RemoveByReposPath(result.repos.Path)
		removeModified = true
	})

	// Handle copy & remove errors
	if copyErr != nil || removeErr != nil {
		return multierror.Append(copyErr, removeErr).ErrorOrNil()
	}

	// Write bundled plugconf file
	content, merr := plugconf.GenerateBundlePlugconf(reposList)
	if merr.ErrorOrNil() != nil {
		// Return vim script parse errors
		return merr
	}
	os.MkdirAll(filepath.Dir(pathutil.BundledPlugConf()), 0755)
	err = ioutil.WriteFile(pathutil.BundledPlugConf(), content, 0644)
	if err != nil {
		return err
	}

	// Copy ftdetect files
	err = os.MkdirAll(filepath.Dir(pathutil.VimVoltFtDetectDir()), 0755)
	if err != nil {
		return err
	}
	for _, repo := range reposList {
		dstFtdPath := pathutil.VimVoltFtDetectDir()
		if !pathutil.Exists(dstFtdPath) {
			os.MkdirAll(dstFtdPath, 0755)
			if !pathutil.Exists(dstFtdPath) {
				return errors.New("could not create " + dstFtdPath)
			}
		}
		src := pathutil.PackReposPathOf(repo.Path)
		srcFtdPath := filepath.Join(src, "ftdetect")
		if err = filepath.Walk(srcFtdPath, func(path string, info os.FileInfo, err error) error {
			if srcFtdPath != path {
				buf := make([]byte, 32*1024)
				err = fileutil.TryLinkFile(path, filepath.Join(dstFtdPath, info.Name()), buf, info.Mode())
				if err != nil {
					return errors.New("could not create " + filepath.Join(dstFtdPath, info.Name()))
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// Write to build-info.json if buildInfo was modified
	if copyModified || removeModified {
		err = buildInfo.Write()
		if err != nil {
			return err
		}
	}

	return nil
}

func (builder *copyBuilder) copyReposList(buildReposMap map[string]*buildinfo.Repos, reposList []lockjson.Repos, optDir, vimExePath string) (chan actionReposResult, int) {
	copyDone := make(chan actionReposResult, len(reposList))
	copyCount := 0
	for i := range reposList {
		if reposList[i].Type == lockjson.ReposGitType {
			n, err := builder.copyReposGit(&reposList[i], buildReposMap[reposList[i].Path], vimExePath, copyDone)
			if err != nil {
				copyDone <- actionReposResult{
					err:   errors.New("failed to copy " + string(reposList[i].Type) + " repos: " + err.Error()),
					repos: &reposList[i],
				}
			}
			copyCount += n
		} else if reposList[i].Type == lockjson.ReposStaticType {
			copyCount += builder.copyReposStatic(&reposList[i], buildReposMap[reposList[i].Path], optDir, vimExePath, copyDone)
		} else {
			copyDone <- actionReposResult{
				err:   errors.New("invalid repository type: " + string(reposList[i].Type)),
				repos: &reposList[i],
			}
		}
	}
	return copyDone, copyCount
}

func (builder *copyBuilder) copyReposGit(repos *lockjson.Repos, buildRepos *buildinfo.Repos, vimExePath string, done chan actionReposResult) (int, error) {
	src := pathutil.FullReposPathOf(repos.Path)

	// Show warning when HEAD and locked revision are different
	head, err := gitutil.GetHEAD(repos.Path)
	if err != nil {
		return 0, fmt.Errorf("failed to get HEAD revision of %q: %s", src, err.Error())
	}
	if head != repos.Version {
		logger.Warnf("%s: HEAD and locked revision are different", repos.Path)
		logger.Warn("  HEAD: " + head)
		logger.Warn("  locked revision: " + repos.Version)
		logger.Warnf("  Please run 'volt get %s' to update locked revision.", repos.Path)
	}

	// Open ~/volt/repos/{repos}
	r, err := git.PlainOpen(src)
	if err != nil {
		return 0, errors.New("failed to open repository: " + err.Error())
	}

	cfg, err := r.Config()
	if err != nil {
		return 0, errors.New("failed to get repository config: " + err.Error())
	}

	isClean := false
	if wt, err := r.Worktree(); err == nil {
		if st, err := wt.Status(); err == nil && st.IsClean() {
			isClean = true
		}
	}

	if builder.hasChangedGitRepos(repos, buildRepos, !isClean) {
		// Copy files from .git/objects/... when:
		// * bare repository
		// * or worktree is clean
		copyFromGitObjects := cfg.Core.IsBare || isClean
		go builder.updateGitRepos(repos, r, copyFromGitObjects, vimExePath, done)
		return 1, nil
	}
	return 0, nil
}

func (builder *copyBuilder) copyReposStatic(repos *lockjson.Repos, buildRepos *buildinfo.Repos, optDir, vimExePath string, done chan actionReposResult) int {
	if builder.hasChangedStaticRepos(repos, buildRepos, optDir) {
		go builder.updateStaticRepos(repos, vimExePath, done)
		return 1
	}
	return 0
}

// Remove vim repos not found in lock.json current repos list
func (builder *copyBuilder) removeReposList(reposList lockjson.ReposList, reposDirList []os.FileInfo) (chan actionReposResult, int) {
	removeList := make([]string, 0, len(reposList))
	for i := range reposDirList {
		reposPath := pathutil.UnpackPathOf(reposDirList[i].Name())
		if !reposList.Contains(reposPath) {
			removeList = append(removeList, reposPath)
		}
	}
	removeDone := make(chan actionReposResult, len(removeList))
	for i := range removeList {
		go func(reposPath string) {
			err := os.RemoveAll(pathutil.PackReposPathOf(reposPath))
			logger.Info("Removing " + reposPath + " ... Done.")
			removeDone <- actionReposResult{
				err:   err,
				repos: &lockjson.Repos{Path: reposPath},
			}
		}(removeList[i])
	}
	return removeDone, len(removeList)
}

func (*copyBuilder) waitCopyRepos(copyDone chan actionReposResult, copyCount int, callback func(*actionReposResult) error) *multierror.Error {
	var merr *multierror.Error
	for i := 0; i < copyCount; i++ {
		result := <-copyDone
		if result.err != nil {
			merr = multierror.Append(
				merr,
				errors.New(
					"failed to copy repository '"+result.repos.Path+
						"': "+result.err.Error()))
		} else {
			err := callback(&result)
			if err != nil {
				merr = multierror.Append(merr, err)
			}
		}
	}
	return merr
}

func (*copyBuilder) constructBuildInfo(buildInfo *buildinfo.BuildInfo, result *actionReposResult) {
	if result.repos.Type == lockjson.ReposGitType {
		r := buildInfo.Repos.FindByReposPath(result.repos.Path)
		if r != nil {
			r.Version = result.repos.Version
			r.Files = result.files
		} else {
			buildInfo.Repos = append(
				buildInfo.Repos,
				buildinfo.Repos{
					Type:    lockjson.ReposGitType,
					Path:    result.repos.Path,
					Version: result.repos.Version,
					Files:   result.files,
				},
			)
		}
	} else if result.repos.Type == lockjson.ReposStaticType {
		r := buildInfo.Repos.FindByReposPath(result.repos.Path)
		if r != nil {
			r.Version = time.Now().Format(time.RFC3339)
			r.Files = result.files
		} else {
			buildInfo.Repos = append(
				buildInfo.Repos,
				buildinfo.Repos{
					Type:    lockjson.ReposStaticType,
					Path:    result.repos.Path,
					Version: time.Now().Format(time.RFC3339),
					Files:   result.files,
				},
			)
		}
	} else {
		logger.Error("Unknown repos type (" + string(result.repos.Type) + ")")
	}
}

func (*copyBuilder) waitRemoveRepos(removeDone chan actionReposResult, removeCount int, callback func(result *actionReposResult)) *multierror.Error {
	var merr *multierror.Error
	for i := 0; i < removeCount; i++ {
		result := <-removeDone
		if result.err != nil {
			target := "files"
			if result.repos != nil {
				target = result.repos.Path
			}
			merr = multierror.Append(
				merr, errors.New(
					"Failed to remove "+target+
						": "+result.err.Error()))
		} else {
			callback(&result)
		}
	}
	return merr
}

func (*copyBuilder) getLatestModTime(path string) (time.Time, error) {
	mtime := time.Unix(0, 0)
	err := filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		t := fi.ModTime()
		if mtime.Before(t) {
			mtime = t
		}
		return nil
	})
	if err != nil {
		return time.Now(), errors.New("failed to readdir: " + err.Error())
	}
	return mtime, nil
}

func (*copyBuilder) hasChangedGitRepos(repos *lockjson.Repos, buildRepos *buildinfo.Repos, isDirty bool) bool {
	if buildRepos == nil { // Full build
		return true
	}
	if repos.Version != buildRepos.Version {
		return true
	}
	if buildRepos.DirtyWorktree || isDirty {
		return true
	}
	return false
}

// Remove ~/.vim/volt/opt/{repos} and copy from ~/volt/repos/{repos}
func (builder *copyBuilder) updateGitRepos(repos *lockjson.Repos, r *git.Repository, copyFromGitObjects bool, vimExePath string, done chan actionReposResult) {
	src := pathutil.FullReposPathOf(repos.Path)
	dst := pathutil.PackReposPathOf(repos.Path)

	// Remove ~/.vim/volt/opt/{repos}
	// TODO: Do not remove here, copy newer files only after
	err := os.RemoveAll(dst)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to remove repository: " + err.Error()),
			repos: repos,
		}
		return
	}

	if copyFromGitObjects {
		logger.Debug("Copy from git objects: " + repos.Path)
		builder.updateBareGitRepos(r, src, dst, repos, vimExePath, done)
	} else {
		logger.Debug("Copy from filesystem: " + repos.Path)
		builder.updateNonBareGitRepos(r, src, dst, repos, vimExePath, done)
	}
}

func (builder *copyBuilder) updateBareGitRepos(r *git.Repository, src, dst string, repos *lockjson.Repos, vimExePath string, done chan actionReposResult) {
	// Get locked commit hash
	commit := plumbing.NewHash(repos.Version)
	commitObj, err := r.CommitObject(commit)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to get HEAD commit object: " + err.Error()),
			repos: repos,
		}
		return
	}

	// Get tree hash of commit hash
	tree, err := r.TreeObject(commitObj.TreeHash)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to get tree " + commit.String() + ": " + err.Error()),
			repos: repos,
		}
		return
	}

	// Copy files
	files := make(buildinfo.FileMap, 512)
	err = tree.Files().ForEach(func(file *object.File) error {
		osMode, err := file.Mode.ToOSFileMode()
		if err != nil {
			return errors.New("failed to convert file mode: " + err.Error())
		}

		contents, err := file.Contents()
		if err != nil {
			return errors.New("failed to get file contents: " + err.Error())
		}

		filename := filepath.Join(dst, file.Name)
		os.MkdirAll(filepath.Dir(filename), 0755)
		ioutil.WriteFile(filename, []byte(contents), osMode)

		files[file.Name] = file.Hash.String() // blob hash
		return nil
	})
	if err != nil {
		done <- actionReposResult{
			err:   err,
			repos: repos,
		}
		return
	}

	// Run ":helptags" to generate tags file
	err = builder.helptags(repos.Path, vimExePath)
	if err != nil {
		done <- actionReposResult{
			err:   err,
			repos: repos,
		}
		return
	}

	done <- actionReposResult{
		err:   nil,
		repos: repos,
		files: files,
	}
}

var BuildModeInvalidType = os.ModeSymlink | os.ModeNamedPipe | os.ModeSocket | os.ModeDevice

func (builder *copyBuilder) updateNonBareGitRepos(r *git.Repository, src, dst string, repos *lockjson.Repos, vimExePath string, done chan actionReposResult) {
	files, err := ioutil.ReadDir(src)
	if err != nil {
		done <- actionReposResult{
			err:   err,
			repos: repos,
		}
		return
	}

	buf := make([]byte, 32*1024)
	created := make(map[string]bool, len(files))
	for _, file := range files {
		// Skip ".git" and ".gitignore"
		if file.Name() == ".git" || file.Name() == ".gitignore" {
			continue
		}
		if file.Mode()&BuildModeInvalidType != 0 {
			// Currenly skip the invalid files...
			continue
		}
		if !created[dst] {
			os.MkdirAll(dst, 0755)
			created[dst] = true
		}
		from := filepath.Join(src, file.Name())
		to := filepath.Join(dst, file.Name())
		var err error
		if file.IsDir() {
			err = fileutil.TryLinkDir(from, to, buf, file.Mode(), BuildModeInvalidType)
		} else {
			err = fileutil.TryLinkFile(from, to, buf, file.Mode())
		}
		if err != nil {
			done <- actionReposResult{
				err:   err,
				repos: repos,
			}
			return
		}
	}

	// Run ":helptags" to generate tags file
	err = builder.helptags(repos.Path, vimExePath)
	if err != nil {
		done <- actionReposResult{
			err:   err,
			repos: repos,
		}
		return
	}

	done <- actionReposResult{
		err:   nil,
		repos: repos,
		files: nil, // all files are overwritten next time even when timestamp is older
	}
}

func (builder *copyBuilder) hasChangedStaticRepos(repos *lockjson.Repos, buildRepos *buildinfo.Repos, optDir string) bool {
	if buildRepos == nil { // Full build
		return true
	}

	src := pathutil.FullReposPathOf(repos.Path)

	// Get latest mtime of src
	// TODO: Don't check mtime here, do it when copy altogether
	srcModTime, err := builder.getLatestModTime(src)
	if err != nil {
		// failed to readdir, do copy again
		return true
	}

	if buildRepos.Version == "" {
		// not found mtime, do copy again
		return true
	}

	// Get latest mtime of dst from build-info.json
	dstModTime, err := time.Parse(time.RFC3339, buildRepos.Version)
	if err != nil {
		// failed to parse datetime, do copy again
		return true
	}

	return dstModTime.Before(srcModTime)
}

// Remove ~/.vim/volt/opt/{repos} and copy from ~/volt/repos/{repos}
func (builder *copyBuilder) updateStaticRepos(repos *lockjson.Repos, vimExePath string, done chan actionReposResult) {
	src := pathutil.FullReposPathOf(repos.Path)
	dst := pathutil.PackReposPathOf(repos.Path)

	// Remove ~/.vim/volt/opt/{repos}
	// TODO: Do not remove here, copy newer files only after
	err := os.RemoveAll(dst)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to remove repository: " + err.Error()),
			repos: repos,
		}
		return
	}

	// Copy ~/volt/repos/{repos} to ~/.vim/volt/opt/{repos}
	buf := make([]byte, 32*1024)
	si, err := os.Stat(src)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to copy static directory: " + err.Error()),
			repos: repos,
		}
		return
	}
	if !si.IsDir() {
		done <- actionReposResult{
			err:   errors.New("failed to copy static directory: source is not a directory"),
			repos: repos,
		}
		return
	}
	err = fileutil.TryLinkDir(src, dst, buf, si.Mode(), BuildModeInvalidType)
	if err != nil {
		done <- actionReposResult{
			err:   errors.New("failed to copy static directory: " + err.Error()),
			repos: repos,
		}
		return
	}

	// Run ":helptags" to generate tags file
	err = builder.helptags(repos.Path, vimExePath)
	if err != nil {
		done <- actionReposResult{
			err:   err,
			repos: repos,
		}
		return
	}

	done <- actionReposResult{
		err:   nil,
		repos: repos,
	}
}
