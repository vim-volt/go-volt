package transaction

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vim-volt/go-volt/pathutil"
)

// Create $VOLTPATH/trx.lock file
func Create() error {
	ownPid := []byte(strconv.Itoa(os.Getpid()))
	trxLockFile := pathutil.TrxLock()

	// Create trx.lock parent directories
	err := os.MkdirAll(filepath.Dir(trxLockFile), 0755)
	if err != nil {
		return err
	}

	// Write pid to trx.lock file
	err = ioutil.WriteFile(trxLockFile, ownPid, 0644)
	if err != nil {
		return err
	}

	// Read pid from trx.lock file
	pid, err := ioutil.ReadFile(trxLockFile)
	if err != nil {
		return err
	}

	if string(pid) != string(ownPid) {
		return errors.New("transaction lock was taken by PID " + string(pid))
	}
	return nil
}

// Remove $VOLTPATH/trx.lock file
func Remove() {
	// Read pid from trx.lock file
	trxLockFile := pathutil.TrxLock()
	pid, err := ioutil.ReadFile(trxLockFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] trx.lock was already removed")
		return
	}

	// Remove trx.lock if pid is same
	if string(pid) != strconv.Itoa(os.Getpid()) {
		fmt.Fprintln(os.Stderr, "[ERROR] Cannot remove another process's trx.lock")
		return
	}
	os.Remove(trxLockFile)
}
