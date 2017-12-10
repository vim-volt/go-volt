package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vim-volt/volt/lockjson"
	"github.com/vim-volt/volt/logger"
	"github.com/vim-volt/volt/transaction"
)

type migrateFlagsType struct {
	helped bool
}

var migrateFlags migrateFlagsType

func init() {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Print(`
Usage
  volt migrate

Description
    Perform migration of $VOLTPATH/lock.json, which means volt converts old version lock.json structure into the latest version. This is always done automatically when reading lock.json content. For example, 'volt get <repos>' will install plugin, and migrate lock.json structure, and write it to lock.json after all. so the migrated content is written to lock.json automatically.
    But, for example, 'volt list' does not write to lock.json but does read, so every time when running 'volt list' shows warning about lock.json is old.
    To suppress this, running this command simply reads and writes migrated structure to lock.json.` + "\n\n")
		//fmt.Println("Options")
		//fs.PrintDefaults()
		fmt.Println()
		migrateFlags.helped = true
	}

	cmdFlagSet["migrate"] = fs
}

type migrateCmd struct{}

func Migrate(args []string) int {
	cmd := migrateCmd{}

	err := cmd.parseArgs(args)
	if err == ErrShowedHelp {
		return 0
	}
	if err != nil {
		logger.Error("Failed to parse args: " + err.Error())
		return 10
	}

	err = cmd.doMigrate()
	if err != nil {
		logger.Error("Failed to migrate: " + err.Error())
		return 11
	}

	return 0
}

func (*migrateCmd) parseArgs(args []string) error {
	fs := cmdFlagSet["migrate"]
	fs.Parse(args)
	if migrateFlags.helped {
		return ErrShowedHelp
	}
	return nil
}

func (cmd *migrateCmd) doMigrate() error {
	// Read lock.json
	lockJSON, err := lockjson.ReadNoMigrationMsg()
	if err != nil {
		return errors.New("could not read lock.json: " + err.Error())
	}

	// Begin transaction
	err = transaction.Create()
	if err != nil {
		return err
	}
	defer transaction.Remove()

	// Write to lock.json
	err = lockJSON.Write()
	if err != nil {
		return errors.New("could not write to lock.json: " + err.Error())
	}
	return nil
}
