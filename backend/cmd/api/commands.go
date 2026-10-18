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
		if flags.Parse(args) != nil || flags.NArg() != 0 || !*confirm || !restoreTarget(dsn) {
			return errors.New("restore requires --backup-id, --confirm-empty-target and a dedicated _restore or _test database")
		}
		return service.Restore(ctx, *id)
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
