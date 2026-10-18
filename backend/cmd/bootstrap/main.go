// Bootstrap creates separate migration and runtime users on a new database.
// It never updates an existing user's password or grants administrator access.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"time"

	"coursek/blog/backend/internal/mysql"
	driver "github.com/go-sql-driver/mysql"
)

func run(ctx context.Context) error {
	database := os.Getenv("DATABASE_NAME")
	if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`).MatchString(database) {
		return errors.New("invalid database name")
	}
	app, migrator := os.Getenv("BLOG_APP_PASSWORD"), os.Getenv("BLOG_MIGRATOR_PASSWORD")
	if len(app) < 24 || len(migrator) < 24 || app == migrator {
		return errors.New("two distinct application passwords are required")
	}
	u := url.URL{Scheme: "mysql", Host: net.JoinHostPort(os.Getenv("DATABASE_HOST"), os.Getenv("DATABASE_PORT")), Path: "/" + database, User: url.UserPassword(os.Getenv("DATABASE_USER"), os.Getenv("DATABASE_PASSWORD"))}
	u.RawQuery = "tls=verify-full"
	config, err := mysql.ConnectionConfig(u.String())
	if err != nil {
		return err
	}
	// CREATE USER cannot use server-side prepared statements. Driver interpolation
	// quotes values using the selected connection charset. Credentials never appear
	// in process arguments, and main suppresses raw server errors.
	config.InterpolateParams = true
	connector, err := driver.NewConnector(config)
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, user := range []struct{ name, password, privileges string }{
		{"blog_migrator", migrator, "SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,DROP,INDEX,REFERENCES,TRIGGER"},
		{"blog_app", app, "SELECT,INSERT,UPDATE,DELETE"},
	} {
		if _, err = db.ExecContext(ctx, "CREATE USER ?@'%' IDENTIFIED BY ?", user.name, user.password); err != nil {
			return err
		}
		// User and privilege names are application constants, never operator-supplied identifiers.
		if _, err = db.ExecContext(ctx, "GRANT "+user.privileges+" ON `"+database+"`.* TO '"+user.name+"'@'%'"); err != nil {
			return err
		}
	}
	return nil
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if run(ctx) != nil {
		fmt.Fprintln(os.Stderr, "Database bootstrap failed; inspect user/grant state before retrying. Raw errors suppressed.")
		os.Exit(1)
	}
}
