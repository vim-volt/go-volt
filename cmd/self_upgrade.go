package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vim-volt/volt/httputil"
	"github.com/vim-volt/volt/logger"
)

type selfUpgradeFlagsType struct {
	helped bool
	check  bool
}

var selfUpgradeFlags selfUpgradeFlagsType

func init() {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Print(`
Usage
  volt self-upgrade [-check]

Description
    Upgrade to the latest volt command, or if -check was given, it only checks the newer version is available.` + "\n\n")
		//fmt.Println("Options")
		//fs.PrintDefaults()
		fmt.Println()
		selfUpgradeFlags.helped = true
	}
	fs.BoolVar(&selfUpgradeFlags.check, "check", false, "only checks the newer version is available")

	cmdFlagSet["self-upgrade"] = fs
}

type selfUpgradeCmd struct{}

func SelfUpgrade(args []string) int {
	cmd := selfUpgradeCmd{}

	flags, err := cmd.parseArgs(args)
	if err == ErrShowedHelp {
		return 0
	}
	if err != nil {
		logger.Error("Failed to parse args: " + err.Error())
		return 10
	}

	if ppidStr := os.Getenv("VOLT_SELF_UPGRADE_PPID"); ppidStr != "" {
		if err = cmd.doCleanUp(ppidStr); err != nil {
			logger.Error("Failed to clean up old binary: " + err.Error())
			return 11
		}
	} else {
		latestURL := "https://api.github.com/repos/vim-volt/volt/releases/latest"
		if err = cmd.doSelfUpgrade(flags, latestURL); err != nil {
			logger.Error("Failed to self-upgrade: " + err.Error())
			return 12
		}
	}

	return 0
}

func (*selfUpgradeCmd) parseArgs(args []string) (*selfUpgradeFlagsType, error) {
	fs := cmdFlagSet["self-upgrade"]
	fs.Parse(args)
	if selfUpgradeFlags.helped {
		return nil, ErrShowedHelp
	}
	return &selfUpgradeFlags, nil
}

func (cmd *selfUpgradeCmd) doCleanUp(ppidStr string) error {
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return errors.New("failed to parse VOLT_SELF_UPGRADE_PPID: " + err.Error())
	}

	// Wait until the parent process exits
	if died := cmd.waitUntilParentExits(ppid); !died {
		return fmt.Errorf("parent pid (%s) is keeping alive for long time", ppidStr)
	}

	// Remove old binary
	voltExe, err := cmd.getExecutablePath()
	if err != nil {
		return err
	}
	return os.Remove(voltExe + ".old")
}

func (cmd *selfUpgradeCmd) waitUntilParentExits(pid int) bool {
	fib := []int{1, 1, 2, 3, 5, 8, 13}
	for i := 0; i < len(fib); i++ {
		if !cmd.processIsAlive(pid) {
			return true
		}
		time.Sleep(time.Duration(fib[i]) * time.Second)
	}
	return false
}

func (*selfUpgradeCmd) processIsAlive(pid int) bool {
	if process, err := os.FindProcess(pid); err != nil {
		return false
	} else {
		err := process.Signal(syscall.Signal(0))
		return err == nil
	}
}

type latestRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []releaseAsset
}

type releaseAsset struct {
	BrowserDownloadURL string `json:"browser_download_url"`
	Name               string `json:"name"`
}

func (cmd *selfUpgradeCmd) doSelfUpgrade(flags *selfUpgradeFlagsType, latestURL string) error {
	// Check the latest binary
	release, err := cmd.check(latestURL)
	if err != nil {
		return err
	}
	logger.Debugf("tag_name = %q", release.TagName)
	tagNameVer, err := parseVersion(release.TagName)
	if err != nil {
		return err
	}
	if compareVersion(tagNameVer, voltVersionInfo) <= 0 {
		logger.Info("No updates were found.")
		return nil
	}
	logger.Infof("Found update: %s -> %s", voltVersion, release.TagName)
	if flags.check {
		// Show release note only when -check was given
		fmt.Println("---")
		fmt.Println(release.Body)
		fmt.Println("---")
		return nil
	}

	// Download the latest binary as "volt[.exe].latest"
	voltExe, err := cmd.getExecutablePath()
	if err != nil {
		return err
	}
	{
		latestFile, err := os.OpenFile(voltExe+".latest", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
		if err != nil {
			return err
		}
		defer latestFile.Close()
		if err = cmd.download(latestFile, release); err != nil {
			return err
		}
	}

	// Rename dir/volt[.exe] to dir/volt[.exe].old
	// NOTE: Windows can rename running executable file
	if err := os.Rename(voltExe, voltExe+".old"); err != nil {
		return err
	}

	// Rename dir/volt[.exe].latest to dir/volt[.exe]
	if err := os.Rename(voltExe+".latest", voltExe); err != nil {
		return err
	}

	// Spawn dir/volt[.exe] with env "VOLT_SELF_UPGRADE_PPID={pid}"
	voltCmd := exec.Command(voltExe, "self-upgrade")
	if err = voltCmd.Start(); err != nil {
		return err
	}
	return nil
}

func (*selfUpgradeCmd) getExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (*selfUpgradeCmd) check(url string) (*latestRelease, error) {
	content, err := httputil.GetContent(url)
	if err != nil {
		return nil, err
	}
	var release latestRelease
	if err = json.Unmarshal(content, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

func (*selfUpgradeCmd) download(w io.Writer, release *latestRelease) error {
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	for i := range release.Assets {
		// e.g.: Name = "volt-v0.1.2-linux-amd64"
		if strings.HasSuffix(release.Assets[i].Name, suffix) {
			r, err := httputil.GetContentReader(release.Assets[i].BrowserDownloadURL)
			if err != nil {
				return err
			}
			defer r.Close()
			if _, err = io.Copy(w, r); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
