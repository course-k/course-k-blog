package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/url"
	"strings"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/mysql"
)

func restoreTarget(dsn string) bool {
	target, err := url.Parse(dsn)
	if err != nil || target.Scheme != "mysql" {
		return false
	}
	database := strings.TrimPrefix(target.Path, "/")
	return strings.HasSuffix(database, "_restore") || strings.HasSuffix(database, "_test")
}

// restoreAllowed accepts a dedicated restore or test database, or any database
// whose server the operator named exactly. The second form exists for moving
// to a new database server: the target is the real database there, and naming
// its host keeps a command aimed at another connection from proceeding. The
// restore itself still refuses a database that is not empty.
func restoreAllowed(dsn, namedHost string) bool {
	if restoreTarget(dsn) {
		return true
	}
	target, err := url.Parse(dsn)
	return err == nil && target.Scheme == "mysql" && namedHost != "" && target.Hostname() == namedHost
}

func runDataCommand(ctx context.Context, command string, args []string, dsn string, store *mysql.Store, service *backup.Service) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch command {
	case "backup":
		if len(args) != 0 {
			return errors.New("backup accepts no arguments")
		}
		return service.Take(ctx, true)
	case "restore":
		id := flags.String("backup-id", "", "")
		confirm := flags.Bool("confirm-empty-target", false, "")
		host := flags.String("new-database-host", "", "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || !*confirm || !restoreAllowed(dsn, *host) {
			return errors.New("restore requires --backup-id, --confirm-empty-target and either a dedicated _restore or _test database or --new-database-host naming the target server")
		}
		return service.Restore(ctx, *id)
	case "clear-restore-target":
		host := flags.String("new-database-host", "", "")
		confirm := flags.Bool("confirm-delete-content", false, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || !*confirm || !restoreAllowed(dsn, *host) {
			return errors.New("clearing requires --confirm-delete-content and either a dedicated _restore or _test database or --new-database-host naming the target server")
		}
		return store.ClearRestoreTarget(ctx)
	case "revoke-sessions":
		confirm := flags.Bool("confirm", false, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || !*confirm {
			return errors.New("session revocation requires --confirm")
		}
		return store.RevokeSessions(ctx)
	default:
		return errors.New("unknown data command")
	}
}
